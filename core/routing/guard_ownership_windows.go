//go:build windows && (amd64 || arm64)

package routing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wfpBaseFilter    = 1
	wfpAttemptFilter = 2
	wfpPolicySchema  = "DobbyVPN network protection schema 1"
	wfpServiceName   = "DobbyVPN Go backend"
)

type WindowsServiceProtection struct {
	mu                 sync.Mutex
	provider, sublayer windows.GUID
	active             *WindowsNetworkGuard
	restore            *WindowsNetworkGuard
	retain             bool
}

var ServiceWindowsProtection = &WindowsServiceProtection{
	provider: windows.GUID{Data1: 0xf6e82586, Data2: 0xa639, Data3: 0x4dae, Data4: [8]byte{0x9c, 0x11, 0x4b, 0x33, 0xf4, 0xc1, 0x41, 0xe2}},
	sublayer: windows.GUID{Data1: 0x5c137d6e, Data2: 0xbba9, Data3: 0x48f1, Data4: [8]byte{0xb6, 0xbc, 0x31, 0xe3, 0xb9, 0xa0, 0x09, 0x51}},
}

type fwpmProvider struct {
	key     windows.GUID
	display fwpmDisplayData
	flags   uint32
	data    wfpByteBlob
	service *uint16
}

type fwpmFilterEnumTemplate struct {
	provider        *windows.GUID
	layer           windows.GUID
	enumType, flags uint32
	context         unsafe.Pointer
	count           uint32
	conditions      *fwpmFilterCondition
	mask            uint32
	callout         *windows.GUID
}

var (
	providerGet       = fwpuclnt.NewProc("FwpmProviderGetByKey0")
	providerAdd       = fwpuclnt.NewProc("FwpmProviderAdd0")
	providerDelete    = fwpuclnt.NewProc("FwpmProviderDeleteByKey0")
	sublayerGet       = fwpuclnt.NewProc("FwpmSubLayerGetByKey0")
	sublayerDelete    = fwpuclnt.NewProc("FwpmSubLayerDeleteByKey0")
	filterEnumCreate  = fwpuclnt.NewProc("FwpmFilterCreateEnumHandle0")
	filterEnum        = fwpuclnt.NewProc("FwpmFilterEnum0")
	filterEnumDestroy = fwpuclnt.NewProc("FwpmFilterDestroyEnumHandle0")
)

func (p *WindowsServiceProtection) openEngine(flags uint32) (*WindowsNetworkGuard, error) {
	g := &WindowsNetworkGuard{policy: p, sublayerKey: p.sublayer}
	name, _ := windows.UTF16PtrFromString("DobbyVPN network protection")
	session := fwpmSession{flags: flags, displayData: fwpmDisplayData{name: name}}
	_, err := windowsDNSWFPCall(windowsDNSWFP.engineOpen, "FwpmEngineOpen0", 0, wfpAuthnWinNT, 0, uintptr(unsafe.Pointer(&session)), uintptr(unsafe.Pointer(&g.engine)))
	runtime.KeepAlive(&session)
	if err != nil || g.engine == 0 {
		return nil, errors.Join(err, errors.New("WFP session acquisition failed"))
	}
	return g, nil
}

func (g *WindowsNetworkGuard) installPolicy() error {
	name, _ := windows.UTF16PtrFromString("DobbyVPN network protection")
	service, _ := windows.UTF16PtrFromString(wfpServiceName)
	marker := []byte(wfpPolicySchema)
	provider := fwpmProvider{key: g.policy.provider, flags: wfpPersistent, display: fwpmDisplayData{name: name}, service: service, data: wfpByteBlob{size: uint32(len(marker)), data: &marker[0]}}
	_, err := windowsDNSWFPCall(providerAdd, providerAdd.Name, uintptr(g.engine), uintptr(unsafe.Pointer(&provider)), 0)
	runtime.KeepAlive(provider)
	if err != nil {
		return err
	}
	layer := fwpmSublayer{sublayerKey: g.sublayerKey, displayData: fwpmDisplayData{name: name}, flags: wfpPersistent, providerKey: &g.policy.provider, weight: ^uint16(0)}
	_, err = windowsDNSWFPCall(windowsDNSWFP.subLayerAdd, "FwpmSubLayerAdd0", uintptr(g.engine), uintptr(unsafe.Pointer(&layer)), 0)
	runtime.KeepAlive(layer)
	return err
}

func (g *WindowsNetworkGuard) ownedPolicy() (owned bool, err error) {
	var provider *fwpmProvider
	code, err := windowsDNSWFPCall(providerGet, providerGet.Name, uintptr(g.engine), uintptr(unsafe.Pointer(&g.policy.provider)), uintptr(unsafe.Pointer(&provider)))
	if code == uintptr(windows.FWP_E_PROVIDER_NOT_FOUND) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, windowsDNSFreeMemory(unsafe.Pointer(&provider))) }()
	if provider == nil || provider.key != g.policy.provider || provider.flags&wfpPersistent == 0 ||
		provider.data.size != uint32(len(wfpPolicySchema)) || provider.data.data == nil ||
		!bytes.Equal(unsafe.Slice(provider.data.data, provider.data.size), []byte(wfpPolicySchema)) ||
		provider.service == nil || windows.UTF16PtrToString(provider.service) != wfpServiceName {
		return false, errors.New("WFP provider ownership or schema mismatch")
	}
	var layer *fwpmSublayer
	_, err = windowsDNSWFPCall(sublayerGet, sublayerGet.Name, uintptr(g.engine), uintptr(unsafe.Pointer(&g.sublayerKey)), uintptr(unsafe.Pointer(&layer)))
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, windowsDNSFreeMemory(unsafe.Pointer(&layer))) }()
	if layer == nil || layer.sublayerKey != g.sublayerKey || layer.providerKey == nil || *layer.providerKey != g.policy.provider || layer.flags&wfpPersistent == 0 {
		return false, errors.New("WFP sublayer ownership or schema mismatch")
	}
	return true, nil
}

func (g *WindowsNetworkGuard) clearPolicyFilters(retainBase bool) error {
	for _, layer := range []windows.GUID{wfpLayerConnectV4, wfpLayerConnectV6} {
		if err := g.clearLayer(layer, retainBase); err != nil {
			return err
		}
	}
	return nil
}

func (g *WindowsNetworkGuard) clearLayer(layer windows.GUID, retainBase bool) (err error) {
	template := fwpmFilterEnumTemplate{provider: &g.policy.provider, layer: layer, mask: ^uint32(0)}
	var handle windows.Handle
	_, err = windowsDNSWFPCall(filterEnumCreate, filterEnumCreate.Name, uintptr(g.engine), uintptr(unsafe.Pointer(&template)), uintptr(unsafe.Pointer(&handle)))
	runtime.KeepAlive(template)
	if err != nil {
		return err
	}
	defer func() {
		_, closeErr := windowsDNSWFPCall(filterEnumDestroy, filterEnumDestroy.Name, uintptr(g.engine), uintptr(handle))
		err = errors.Join(err, closeErr)
	}()
	for {
		var entries **fwpmFilter
		var count uint32
		_, err = windowsDNSWFPCall(filterEnum, filterEnum.Name, uintptr(g.engine), uintptr(handle), 256, uintptr(unsafe.Pointer(&entries)), uintptr(unsafe.Pointer(&count)))
		if err != nil {
			return err
		}
		if count > 256 || count > 0 && entries == nil {
			return errors.New("invalid WFP enumeration response")
		}
		var ids []uint64
		for _, filter := range unsafe.Slice(entries, count) {
			if filter == nil || filter.providerKey == nil || *filter.providerKey != g.policy.provider || filter.sublayerKey != g.sublayerKey ||
				filter.providerData.size != 1 || filter.providerData.data == nil || (*filter.providerData.data != wfpBaseFilter && *filter.providerData.data != wfpAttemptFilter) {
				err = errors.New("WFP filter ownership or schema mismatch")
				break
			}
			if !retainBase || *filter.providerData.data == wfpAttemptFilter {
				ids = append(ids, filter.filterID)
			}
		}
		if entries != nil {
			err = errors.Join(err, windowsDNSFreeMemory(unsafe.Pointer(&entries)))
		}
		if err != nil {
			return err
		}
		if err = g.deleteIDs(ids); err != nil {
			return err
		}
		if count < 256 {
			return nil
		}
	}
}

func (g *WindowsNetworkGuard) reconcile(retainBase bool) error {
	if err := g.clearPolicyFilters(retainBase); err != nil {
		return err
	}
	if retainBase {
		return nil
	}
	if _, err := windowsDNSWFPCall(sublayerDelete, sublayerDelete.Name, uintptr(g.engine), uintptr(unsafe.Pointer(&g.sublayerKey))); err != nil {
		return err
	}
	_, err := windowsDNSWFPCall(providerDelete, providerDelete.Name, uintptr(g.engine), uintptr(unsafe.Pointer(&g.policy.provider)))
	return err
}

func (p *WindowsServiceProtection) reconcileDetached(ctx context.Context, retain bool) (owned bool, err error) {
	g, err := p.openEngine(0)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, g.closeEngine()) }()
	err = g.transaction(ctx, func() error {
		var err error
		owned, err = g.ownedPolicy()
		if err != nil || !owned {
			return err
		}
		return g.reconcile(retain)
	})
	return owned, err
}

// OpenRestoreGuard permits a subscription fetch while the previous tunnel is
// absent. Its dynamic WFP session removes its filters on close or process loss.
func (p *WindowsServiceProtection) OpenRestoreGuard(ctx context.Context) (*WindowsNetworkGuard, func() error, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if !p.retain || p.active != nil || p.restore != nil {
		return nil, nil, errors.New("retained WFP policy unavailable for subscription recovery")
	}
	const fwpmSessionFlagDynamic = 1
	g, err := p.openEngine(fwpmSessionFlagDynamic)
	if err != nil {
		return nil, nil, err
	}
	exe, err := os.Executable()
	if err == nil {
		g.appID, err = windowsApplicationID(exe)
	}
	if err != nil {
		return nil, nil, errors.Join(err, g.closeEngine())
	}
	g.attempts = make(map[uint64]map[string]uint64)
	p.restore = g
	release := func() error {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.restore != g {
			return nil
		}
		return p.closeRestoreLocked()
	}
	return g, release, nil
}

func (p *WindowsServiceProtection) closeRestoreLocked() error {
	g := p.restore
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.closeEngine(); err != nil {
		return err
	}
	p.restore = nil
	return nil
}

// Install the base with no tunnel permit when a saved activation outlives
// its policy (for example on upgrade from a dynamic-only guard).
func (p *WindowsServiceProtection) PrepareRestore(ctx context.Context) (err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active != nil || p.restore != nil {
		return errors.New("cannot replace an active WFP guard during startup")
	}
	g, err := p.openEngine(0)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, g.closeEngine()) }()
	if err = g.transaction(ctx, g.replaceBase); err != nil {
		return err
	}
	p.retain = true
	return nil
}

func (p *WindowsServiceProtection) Recover(ctx context.Context) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	owned, err := p.reconcileDetached(ctx, true)
	p.retain = owned
	return owned, err
}

func (p *WindowsServiceProtection) SetEnabled(ctx context.Context, enabled bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.retain = enabled
	if !enabled {
		if err := p.closeRestoreLocked(); err != nil {
			return err
		}
	}
	if enabled || p.active != nil {
		return nil
	}
	_, err := p.reconcileDetached(ctx, false)
	if err != nil {
		return fmt.Errorf("release persistent WFP policy: %w", err)
	}
	return nil
}
