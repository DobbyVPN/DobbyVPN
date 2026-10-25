//go:build windows && (amd64 || arm64)

package routing

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"unsafe"

	"core/log"

	"golang.org/x/sys/windows"
)

const (
	wfpAuthnWinNT      = 10
	wfpSessionDynamic  = 1
	wfpUint8           = 1
	wfpUint16          = 2
	wfpUint64          = 4
	wfpByteBlobType    = 12
	wfpFilterWeightMax = 15
	wfpFilterWeight    = wfpFilterWeightMax
	wfpMatchEqual      = 0
	wfpMatchNotEqual   = 10
	wfpActionBlock     = 0x1001
	wfpProtocolTCP     = 6
	wfpProtocolUDP     = 17
	wfpDNSPort         = 53
)

var (
	wfpConditionIPProtocol = windows.GUID{Data1: 0x3971ef2b, Data2: 0x623e, Data3: 0x4f9a, Data4: [8]byte{0x8c, 0xb1, 0x6e, 0x79, 0xb8, 0x06, 0xb9, 0xa7}}
	wfpConditionRemotePort = windows.GUID{Data1: 0xc35a604d, Data2: 0xd22b, Data3: 0x4e1a, Data4: [8]byte{0x91, 0xb4, 0x68, 0xf6, 0x74, 0xee, 0x67, 0x4b}}
	wfpConditionNextHop    = windows.GUID{Data1: 0x93ae8f5b, Data2: 0x7f6f, Data3: 0x4719, Data4: [8]byte{0x98, 0xc8, 0x14, 0xe9, 0x74, 0x29, 0xef, 0x04}}
	wfpConditionAppID      = windows.GUID{Data1: 0xd78e1e87, Data2: 0x8644, Data3: 0x4ea5, Data4: [8]byte{0x94, 0x37, 0xd8, 0x09, 0xec, 0xef, 0xc9, 0x71}}
	wfpLayerConnectV4      = windows.GUID{Data1: 0xc38d57d1, Data2: 0x05a7, Data3: 0x4c33, Data4: [8]byte{0x90, 0x4f, 0x7f, 0xbc, 0xee, 0xe6, 0x0e, 0x82}}
	wfpLayerConnectV6      = windows.GUID{Data1: 0x4a72393b, Data2: 0x319f, Data3: 0x44bc, Data4: [8]byte{0x84, 0xc3, 0xba, 0x54, 0xdc, 0xb3, 0xb6, 0xb4}}
)

var fwpuclnt = windows.NewLazySystemDLL("fwpuclnt.dll")

var windowsDNSWFP = struct {
	engineOpen        *windows.LazyProc
	engineClose       *windows.LazyProc
	transactionBegin  *windows.LazyProc
	transactionCommit *windows.LazyProc
	transactionAbort  *windows.LazyProc
	subLayerAdd       *windows.LazyProc
	filterAdd         *windows.LazyProc
	getAppID          *windows.LazyProc
	freeMemory        *windows.LazyProc
}{
	engineOpen:        fwpuclnt.NewProc("FwpmEngineOpen0"),
	engineClose:       fwpuclnt.NewProc("FwpmEngineClose0"),
	transactionBegin:  fwpuclnt.NewProc("FwpmTransactionBegin0"),
	transactionCommit: fwpuclnt.NewProc("FwpmTransactionCommit0"),
	transactionAbort:  fwpuclnt.NewProc("FwpmTransactionAbort0"),
	subLayerAdd:       fwpuclnt.NewProc("FwpmSubLayerAdd0"),
	filterAdd:         fwpuclnt.NewProc("FwpmFilterAdd0"),
	getAppID:          fwpuclnt.NewProc("FwpmGetAppIdFromFileName0"),
	freeMemory:        fwpuclnt.NewProc("FwpmFreeMemory0"),
}

type wfpByteBlob struct {
	size uint32
	data *byte
}

type wfpValue struct {
	dataType uint32
	value    uintptr
}

type fwpmDisplayData struct {
	name        *uint16
	description *uint16
}

type fwpmSession struct {
	sessionKey       windows.GUID
	displayData      fwpmDisplayData
	flags            uint32
	txnWaitTimeoutMS uint32
	processID        uint32
	sid              *windows.SID
	username         *uint16
	kernelMode       int32
}

type fwpmSublayer struct {
	sublayerKey  windows.GUID
	displayData  fwpmDisplayData
	flags        uint32
	providerKey  *windows.GUID
	providerData wfpByteBlob
	weight       uint16
}

type fwpmFilterCondition struct {
	fieldKey       windows.GUID
	matchType      uint32
	conditionValue wfpValue
}

type fwpmAction struct {
	actionType uint32
	calloutKey windows.GUID
}

// These layouts match FWPM_FILTER0 and its nested 64-bit WFP types. The WFP
// structures use pointer-sized alignment on both supported 64-bit Windows
// architectures.
type fwpmFilter struct {
	filterKey           windows.GUID
	displayData         fwpmDisplayData
	flags               uint32
	providerKey         *windows.GUID
	providerData        wfpByteBlob
	layerKey            windows.GUID
	sublayerKey         windows.GUID
	weight              wfpValue
	numFilterConditions uint32
	filterCondition     *fwpmFilterCondition
	action              fwpmAction
	_                   [4]byte
	providerContextKey  windows.GUID
	reserved            *windows.GUID
	filterID            uint64
	effectiveWeight     wfpValue
}

// WindowsDNSGuard owns one dynamic WFP engine session. Closing the session
// removes only its sublayer and filters; Windows also removes them on process exit.
type WindowsDNSGuard struct {
	mu          sync.Mutex
	engine      windows.Handle
	sublayerKey windows.GUID
	filterKeys  [4]windows.GUID
	filterIDs   [4]uint64
	tunnelLUID  uint64
}

// OpenWindowsDNSGuard blocks TCP and UDP DNS on every next-hop interface other
// than the tunnel, except for connections from the current backend executable.
func OpenWindowsDNSGuard(tunnelLUID uint64) (*WindowsDNSGuard, error) {
	if tunnelLUID == 0 {
		return nil, errors.New("open Windows DNS guard: tunnel LUID is zero")
	}

	sessionName, err := windows.UTF16PtrFromString("DobbyVPN DNS guard")
	if err != nil {
		return nil, fmt.Errorf("encode WFP session name: %w", err)
	}
	session := fwpmSession{
		displayData: fwpmDisplayData{name: sessionName},
		flags:       wfpSessionDynamic,
		// txnWaitTimeoutMS stays zero so BFE uses its documented default.
	}
	guard := &WindowsDNSGuard{tunnelLUID: tunnelLUID}
	_, err = windowsDNSWFPCall(windowsDNSWFP.engineOpen, "FwpmEngineOpen0", 0, wfpAuthnWinNT, 0, uintptr(unsafe.Pointer(&session)), uintptr(unsafe.Pointer(&guard.engine)))
	runtime.KeepAlive(&session)
	if err != nil {
		if guard.engine == 0 {
			log.Error(Category, "Windows DNS guard acquisition failed", map[string]any{"operation": "FwpmEngineOpen0", "cause": err.Error()})
			return nil, err
		}
		return guard.failInstall(err, false)
	}
	if guard.engine == 0 {
		err := errors.New("FwpmEngineOpen0 succeeded without an engine handle")
		log.Error(Category, "Windows DNS guard acquisition failed", map[string]any{"operation": "FwpmEngineOpen0", "cause": err.Error()})
		return nil, err
	}

	guard.sublayerKey, err = windows.GenerateGUID()
	if err != nil {
		return guard.failInstall(fmt.Errorf("generate WFP sublayer ID: %w", err), false)
	}
	for i := range guard.filterKeys {
		guard.filterKeys[i], err = windows.GenerateGUID()
		if err != nil {
			return guard.failInstall(fmt.Errorf("generate WFP filter ID %d: %w", i, err), false)
		}
	}

	executable, err := os.Executable()
	if err != nil {
		return guard.failInstall(fmt.Errorf("resolve backend executable for WFP app ID: %w", err), false)
	}
	executableUTF16, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return guard.failInstall(fmt.Errorf("encode backend executable for WFP app ID: %w", err), false)
	}
	var appID *wfpByteBlob
	_, err = windowsDNSWFPCall(windowsDNSWFP.getAppID, "FwpmGetAppIdFromFileName0", uintptr(unsafe.Pointer(executableUTF16)), uintptr(unsafe.Pointer(&appID)))
	runtime.KeepAlive(executableUTF16)
	if err != nil {
		freeErr := windowsDNSFreeMemory(unsafe.Pointer(&appID))
		return guard.failInstall(errors.Join(err, freeErr), false)
	}
	if appID == nil || appID.size == 0 || appID.data == nil {
		freeErr := windowsDNSFreeMemory(unsafe.Pointer(&appID))
		return guard.failInstall(errors.Join(errors.New("FwpmGetAppIdFromFileName0 returned an empty app ID"), freeErr), false)
	}
	defer func() {
		if err := windowsDNSFreeMemory(unsafe.Pointer(&appID)); err != nil {
			log.Error(Category, "Windows DNS guard app ID cleanup failed", map[string]any{"sublayer_id": guard.sublayerKey.String(), "cause": err.Error()})
		}
	}()

	sublayerName, err := windows.UTF16PtrFromString("DobbyVPN DNS leak guard")
	if err != nil {
		return guard.failInstall(fmt.Errorf("encode WFP sublayer name: %w", err), false)
	}
	sublayerDescription, err := windows.UTF16PtrFromString("Blocks physical-interface DNS outside the backend process")
	if err != nil {
		return guard.failInstall(fmt.Errorf("encode WFP sublayer description: %w", err), false)
	}
	sublayer := fwpmSublayer{
		sublayerKey: guard.sublayerKey,
		displayData: fwpmDisplayData{name: sublayerName, description: sublayerDescription},
		weight:      ^uint16(0),
	}

	if _, err = windowsDNSWFPCall(windowsDNSWFP.transactionBegin, "FwpmTransactionBegin0", uintptr(guard.engine), 0); err != nil {
		return guard.failInstall(err, false)
	}
	if _, err = windowsDNSWFPCall(windowsDNSWFP.subLayerAdd, "FwpmSubLayerAdd0", uintptr(guard.engine), uintptr(unsafe.Pointer(&sublayer)), 0); err != nil {
		return guard.failInstall(err, true)
	}
	if err = guard.addDNSFilters(appID); err != nil {
		return guard.failInstall(err, true)
	}
	if _, err = windowsDNSWFPCall(windowsDNSWFP.transactionCommit, "FwpmTransactionCommit0", uintptr(guard.engine)); err != nil {
		return guard.failInstall(err, true)
	}
	runtime.KeepAlive(&sublayer)
	runtime.KeepAlive(appID)
	log.Info(Category, "Windows DNS guard acquired", map[string]any{
		"sublayer_id": guard.sublayerKey.String(), "filter_ids": guard.filterIDs[:], "tunnel_luid": tunnelLUID,
	})
	return guard, nil
}

func (g *WindowsDNSGuard) addDNSFilters(appID *wfpByteBlob) error {
	layers := [2]windows.GUID{wfpLayerConnectV4, wfpLayerConnectV6}
	protocols := [2]uint8{wfpProtocolUDP, wfpProtocolTCP}
	index := 0
	for family, layer := range layers {
		for _, protocol := range protocols {
			protocolValue := protocol
			portValue := uint16(wfpDNSPort)
			conditions := [4]fwpmFilterCondition{
				{fieldKey: wfpConditionIPProtocol, matchType: wfpMatchEqual, conditionValue: wfpValue{dataType: wfpUint8, value: uintptr(protocolValue)}},
				{fieldKey: wfpConditionRemotePort, matchType: wfpMatchEqual, conditionValue: wfpValue{dataType: wfpUint16, value: uintptr(portValue)}},
				{fieldKey: wfpConditionNextHop, matchType: wfpMatchNotEqual, conditionValue: wfpValue{dataType: wfpUint64, value: uintptr(unsafe.Pointer(&g.tunnelLUID))}},
				// FWP_MATCH_NOT_EQUAL is valid for sortable FWP_BYTE_BLOB_TYPE values on Windows 7 and later.
				{fieldKey: wfpConditionAppID, matchType: wfpMatchNotEqual, conditionValue: wfpValue{dataType: wfpByteBlobType, value: uintptr(unsafe.Pointer(appID))}},
			}
			familyName := "IPv4"
			if family == 1 {
				familyName = "IPv6"
			}
			protocolName := "UDP"
			if protocol == wfpProtocolTCP {
				protocolName = "TCP"
			}
			name, err := windows.UTF16PtrFromString("Block physical " + familyName + " " + protocolName + " DNS")
			if err != nil {
				return fmt.Errorf("encode WFP filter name %d: %w", index, err)
			}
			filter := fwpmFilter{
				filterKey:           g.filterKeys[index],
				displayData:         fwpmDisplayData{name: name},
				layerKey:            layer,
				sublayerKey:         g.sublayerKey,
				weight:              wfpValue{dataType: wfpUint8, value: uintptr(wfpFilterWeight)},
				numFilterConditions: uint32(len(conditions)),
				filterCondition:     &conditions[0],
				action:              fwpmAction{actionType: wfpActionBlock},
			}
			_, addErr := windowsDNSWFPCall(windowsDNSWFP.filterAdd, "FwpmFilterAdd0", uintptr(g.engine), uintptr(unsafe.Pointer(&filter)), 0, uintptr(unsafe.Pointer(&g.filterIDs[index])))
			runtime.KeepAlive(g)
			runtime.KeepAlive(&filter)
			runtime.KeepAlive(appID)
			if addErr != nil {
				return fmt.Errorf("add WFP DNS filter %s/%s id=%s: %w", familyName, protocolName, filter.filterKey.String(), addErr)
			}
			index++
		}
	}
	return nil
}

func (g *WindowsDNSGuard) failInstall(cause error, transaction bool) (*WindowsDNSGuard, error) {
	cleanupErrors := []error{cause}
	if transaction {
		if _, err := windowsDNSWFPCall(windowsDNSWFP.transactionAbort, "FwpmTransactionAbort0", uintptr(g.engine)); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	if err := g.Close(); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("close partially installed Windows DNS guard sublayer=%s: %w", g.sublayerKey.String(), err))
		combined := errors.Join(cleanupErrors...)
		log.Error(Category, "Windows DNS guard acquisition failed", map[string]any{"sublayer_id": g.sublayerKey.String(), "filter_ids": g.filterIDs[:], "cause": combined.Error()})
		return g, combined
	}
	combined := errors.Join(cleanupErrors...)
	log.Error(Category, "Windows DNS guard acquisition failed", map[string]any{"sublayer_id": g.sublayerKey.String(), "filter_ids": g.filterIDs[:], "cause": combined.Error()})
	return nil, combined
}

// Close removes this guard's dynamic WFP session. It is safe to call repeatedly;
// a failed close retains the handle so the owner can retry cleanup.
func (g *WindowsDNSGuard) Close() error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.engine == 0 {
		return nil
	}
	if _, err := windowsDNSWFPCall(windowsDNSWFP.engineClose, "FwpmEngineClose0", uintptr(g.engine)); err != nil {
		log.Error(Category, "Windows DNS guard removal failed", map[string]any{
			"sublayer_id": g.sublayerKey.String(), "filter_ids": g.filterIDs[:], "cause": err.Error(),
		})
		return err
	}
	g.engine = 0
	log.Info(Category, "Windows DNS guard removed", map[string]any{"sublayer_id": g.sublayerKey.String(), "filter_ids": g.filterIDs[:]})
	return nil
}

//go:uintptrescapes
func windowsDNSWFPCall(proc *windows.LazyProc, operation string, args ...uintptr) (uintptr, error) {
	if err := proc.Find(); err != nil {
		return 0, fmt.Errorf("%s unavailable: %w", operation, err)
	}
	status, _, _ := proc.Call(args...)
	if status != 0 {
		return status, fmt.Errorf("%s returned WFP status 0x%08x", operation, uint32(status))
	}
	return 0, nil
}

func windowsDNSFreeMemory(pointer unsafe.Pointer) error {
	if pointer == nil {
		return nil
	}
	if err := windowsDNSWFP.freeMemory.Find(); err != nil {
		return fmt.Errorf("FwpmFreeMemory0 unavailable: %w", err)
	}
	windowsDNSWFP.freeMemory.Call(uintptr(pointer))
	return nil
}
