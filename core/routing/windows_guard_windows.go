//go:build windows && (amd64 || arm64)

package routing

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"runtime"
	"strconv"
	"sync"
	"unsafe"

	"core/log"

	"golang.org/x/sys/windows"
)

const (
	wfpAuthnWinNT         = 10
	wfpSessionDynamic     = 1
	wfpUint8              = 1
	wfpUint16             = 2
	wfpUint64             = 4
	wfpByteBlobType       = 12
	wfpFilterWeightMax    = 15
	wfpFilterWeight       = wfpFilterWeightMax
	wfpMatchEqual         = 0
	wfpMatchNotEqual      = 10
	wfpActionBlock        = 0x1001
	wfpProtocolTCP        = 6
	wfpProtocolUDP        = 17
	wfpDNSPort            = 53
	wfpUint32             = 3
	wfpByteArray16        = 11
	wfpSecurityDescriptor = 14
	wfpActionPermit       = 0x1002
	wfpMatchFlagsAllSet   = 6
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
	filterDelete      *windows.LazyProc
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
	filterDelete:      fwpuclnt.NewProc("FwpmFilterDeleteById0"),
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

var (
	wfpConditionRemoteAddress = windows.GUID{Data1: 0xb235ae9a, Data2: 0x1d64, Data3: 0x49b8, Data4: [8]byte{0xa4, 0x4c, 0x5f, 0xf3, 0xd9, 0x09, 0x50, 0x45}}
	wfpConditionLocalPort     = windows.GUID{Data1: 0x0c1ba1af, Data2: 0x5765, Data3: 0x453f, Data4: [8]byte{0xaf, 0x22, 0xa8, 0xf7, 0x91, 0xac, 0x77, 0x5b}}
	wfpConditionFlags         = windows.GUID{Data1: 0x632ce23b, Data2: 0x5167, Data3: 0x435c, Data4: [8]byte{0x86, 0xd7, 0xe9, 0x03, 0x68, 0x4a, 0xa8, 0x0c}}
	wfpConditionUser          = windows.GUID{Data1: 0xaf043a0a, Data2: 0xb34d, Data3: 0x4f86, Data4: [8]byte{0x97, 0x9c, 0xc9, 0x03, 0x71, 0xaf, 0x6e, 0x66}}
)

// WindowsNetworkGuard owns one dynamic WFP session from network activation to
// Stop. Only the retained tunnel, loopback, required system traffic, and actual
// backend destinations are permitted. There is no executable-wide exemption.
// Process termination removes the session through Windows, as for Wintun.
type WindowsNetworkGuard struct {
	mu                sync.Mutex
	engine            windows.Handle
	sublayerKey       windows.GUID
	filterIDs         []uint64
	tunnelLUID        uint64
	appID             []byte
	attempts          map[uint64]map[string]uint64
	retiredThrough    uint64
	bootstrapIdentity string
	bootstrapIDs      []uint64
}

func OpenWindowsNetworkGuard(tunnelLUID uint64) (*WindowsNetworkGuard, error) {
	if tunnelLUID == 0 {
		return nil, errors.New("open Windows network guard: tunnel LUID is zero")
	}
	g := &WindowsNetworkGuard{tunnelLUID: tunnelLUID, attempts: make(map[uint64]map[string]uint64)}
	sessionName, _ := windows.UTF16PtrFromString("DobbyVPN network protection")
	session := fwpmSession{displayData: fwpmDisplayData{name: sessionName}, flags: wfpSessionDynamic}
	_, err := windowsDNSWFPCall(windowsDNSWFP.engineOpen, "FwpmEngineOpen0", 0, wfpAuthnWinNT, 0, uintptr(unsafe.Pointer(&session)), uintptr(unsafe.Pointer(&g.engine)))
	runtime.KeepAlive(&session)
	if err != nil || g.engine == 0 {
		return g.failInstall(errors.Join(err, errors.New("WFP network session acquisition failed")))
	}
	g.sublayerKey, err = windows.GenerateGUID()
	if err != nil {
		return g.failInstall(err)
	}
	exe, err := os.Executable()
	if err != nil {
		return g.failInstall(err)
	}
	g.appID, err = windowsApplicationID(exe)
	if err != nil {
		return g.failInstall(err)
	}
	err = g.transaction(context.Background(), func() error {
		name, _ := windows.UTF16PtrFromString("DobbyVPN network protection")
		layer := fwpmSublayer{sublayerKey: g.sublayerKey, displayData: fwpmDisplayData{name: name}, weight: ^uint16(0)}
		_, err := windowsDNSWFPCall(windowsDNSWFP.subLayerAdd, "FwpmSubLayerAdd0", uintptr(g.engine), uintptr(unsafe.Pointer(&layer)), 0)
		runtime.KeepAlive(&layer)
		if err != nil {
			return err
		}
		return g.installBase()
	})
	if err != nil {
		return g.failInstall(err)
	}
	log.Info(Category, "Windows network protection acquired", map[string]any{"sublayer_id": g.sublayerKey.String(), "filter_ids": g.filterIDs, "tunnel_luid": tunnelLUID})
	return g, nil
}

// Conditions retain all Go storage referenced by native WFP unions until Add
// returns. A uintptr alone does not keep its referent alive across a syscall.
type wfpConditions struct {
	values []fwpmFilterCondition
	roots  []any
}

func (c *wfpConditions) scalar(key windows.GUID, kind uint32, value uintptr) {
	c.values = append(c.values, fwpmFilterCondition{fieldKey: key, matchType: wfpMatchEqual, conditionValue: wfpValue{dataType: kind, value: value}})
}
func (c *wfpConditions) integer64(key windows.GUID, value uint64) {
	p := new(uint64)
	*p = value
	c.roots = append(c.roots, p)
	c.scalar(key, wfpUint64, uintptr(unsafe.Pointer(p)))
}
func (c *wfpConditions) blob(key windows.GUID, kind uint32, data []byte) {
	b := &wfpByteBlob{size: uint32(len(data)), data: &data[0]}
	c.roots = append(c.roots, data, b)
	c.scalar(key, kind, uintptr(unsafe.Pointer(b)))
}
func (c *wfpConditions) remote(address netip.Addr) windows.GUID {
	address = address.Unmap()
	if address.Is4() {
		b := address.As4()
		c.scalar(wfpConditionRemoteAddress, wfpUint32, uintptr(binary.BigEndian.Uint32(b[:])))
		return wfpLayerConnectV4
	}
	b := new([16]byte)
	*b = address.As16()
	c.roots = append(c.roots, b)
	c.scalar(wfpConditionRemoteAddress, wfpByteArray16, uintptr(unsafe.Pointer(b)))
	return wfpLayerConnectV6
}
func (g *WindowsNetworkGuard) add(name string, layer windows.GUID, action uint32, weight uint8, c *wfpConditions) (uint64, error) {
	key, err := windows.GenerateGUID()
	if err != nil {
		return 0, err
	}
	label, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	filter := fwpmFilter{filterKey: key, displayData: fwpmDisplayData{name: label}, layerKey: layer, sublayerKey: g.sublayerKey, weight: wfpValue{dataType: wfpUint8, value: uintptr(weight)}, action: fwpmAction{actionType: action}}
	if len(c.values) > 0 {
		filter.numFilterConditions = uint32(len(c.values))
		filter.filterCondition = &c.values[0]
	}
	var id uint64
	_, err = windowsDNSWFPCall(windowsDNSWFP.filterAdd, "FwpmFilterAdd0 "+name, uintptr(g.engine), uintptr(unsafe.Pointer(&filter)), 0, uintptr(unsafe.Pointer(&id)))
	runtime.KeepAlive(filter)
	runtime.KeepAlive(c)
	if err != nil {
		return 0, err
	}
	// IDs are diagnostic identities of the base policy. Attempt/bootstrap IDs
	// are tracked by their resource owner and removed transactionally.
	return id, nil
}
func (g *WindowsNetworkGuard) base(name string, layer windows.GUID, action uint32, weight uint8, c *wfpConditions) error {
	id, err := g.add(name, layer, action, weight, c)
	if err == nil {
		g.filterIDs = append(g.filterIDs, id)
	}
	return err
}
func (g *WindowsNetworkGuard) installBase() error {
	for _, layer := range []windows.GUID{wfpLayerConnectV4, wfpLayerConnectV6} {
		if err := g.base("Block non-tunnel traffic", layer, wfpActionBlock, 0, &wfpConditions{}); err != nil {
			return err
		}
		loop := &wfpConditions{}
		loop.scalar(wfpConditionFlags, wfpUint32, 1)
		loop.values[0].matchType = wfpMatchFlagsAllSet
		if err := g.base("Permit loopback", layer, wfpActionPermit, 15, loop); err != nil {
			return err
		}
		tun := &wfpConditions{}
		tun.integer64(wfpConditionNextHop, g.tunnelLUID)
		if err := g.base("Permit retained VPN interface", layer, wfpActionPermit, 15, tun); err != nil {
			return err
		}
	}
	// A service SID confines DHCP permissions to the Windows DHCP client. The
	// exact client/server ports permit renewals as well as initial discovery.
	dhcp, err := windowsServiceDescriptor("Dhcp")
	if err != nil {
		return err
	}
	for _, family := range []struct {
		layer          windows.GUID
		client, server uint16
	}{{wfpLayerConnectV4, 68, 67}, {wfpLayerConnectV6, 546, 547}} {
		c := &wfpConditions{}
		c.blob(wfpConditionUser, wfpSecurityDescriptor, dhcp)
		c.scalar(wfpConditionIPProtocol, wfpUint8, wfpProtocolUDP)
		c.scalar(wfpConditionLocalPort, wfpUint16, uintptr(family.client))
		c.scalar(wfpConditionRemotePort, wfpUint16, uintptr(family.server))
		if err := g.base("Permit DHCP client", family.layer, wfpActionPermit, 15, c); err != nil {
			return err
		}
	}
	// ALE represents ICMP type/code through the local/remote port conditions.
	// Router/neighbor discovery is required to keep the physical IPv6 link alive.
	for _, kind := range []uintptr{133, 135, 136} {
		c := &wfpConditions{}
		c.scalar(wfpConditionIPProtocol, wfpUint8, 58)
		c.scalar(wfpConditionLocalPort, wfpUint16, kind)
		c.scalar(wfpConditionRemotePort, wfpUint16, 0)
		if kind == 133 {
			c.remote(netip.MustParseAddr("ff02::2"))
		}
		if err := g.base("Permit IPv6 neighbor discovery", wfpLayerConnectV6, wfpActionPermit, 15, c); err != nil {
			return err
		}
	}
	return nil
}

func windowsServiceDescriptor(service string) ([]byte, error) {
	sid, _, _, err := windows.LookupSID("", `NT SERVICE\`+service)
	if err != nil {
		return nil, fmt.Errorf("resolve %s service SID: %w", service, err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:(A;;0x1;;;" + sid.String() + ")")
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), unsafe.Slice((*byte)(unsafe.Pointer(sd)), sd.Length())...), nil
}
func windowsApplicationID(path string) (data []byte, err error) {
	pathUTF16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var blob *wfpByteBlob
	_, err = windowsDNSWFPCall(windowsDNSWFP.getAppID, "FwpmGetAppIdFromFileName0", uintptr(unsafe.Pointer(pathUTF16)), uintptr(unsafe.Pointer(&blob)))
	runtime.KeepAlive(pathUTF16)
	defer func() { err = errors.Join(err, windowsDNSFreeMemory(unsafe.Pointer(&blob))) }()
	if err != nil {
		return nil, err
	}
	if blob == nil || blob.size == 0 || blob.data == nil {
		return nil, errors.New("Windows application ID is empty")
	}
	return append([]byte(nil), unsafe.Slice(blob.data, blob.size)...), nil
}
func (g *WindowsNetworkGuard) transaction(ctx context.Context, change func() error) (err error) {
	if err = ctx.Err(); err != nil {
		return err
	}
	if _, err = windowsDNSWFPCall(windowsDNSWFP.transactionBegin, "FwpmTransactionBegin0", uintptr(g.engine), 0); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, abortErr := windowsDNSWFPCall(windowsDNSWFP.transactionAbort, "FwpmTransactionAbort0", uintptr(g.engine))
			err = errors.Join(err, abortErr)
		}
	}()
	if err = change(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	_, err = windowsDNSWFPCall(windowsDNSWFP.transactionCommit, "FwpmTransactionCommit0", uintptr(g.engine))
	committed = err == nil
	return err
}
func (g *WindowsNetworkGuard) deleteIDs(ids []uint64) error {
	for _, id := range ids {
		if _, err := windowsDNSWFPCall(windowsDNSWFP.filterDelete, "FwpmFilterDeleteById0", uintptr(g.engine), uintptr(id)); err != nil {
			return err
		}
	}
	return nil
}

func windowsTransportDestination(network, address string) (netip.Addr, uint16, uint8, error) {
	var protocol uint8
	switch network {
	case "tcp4", "tcp6":
		protocol = wfpProtocolTCP
	case "udp4", "udp6":
		protocol = wfpProtocolUDP
	default:
		return netip.Addr{}, 0, 0, fmt.Errorf("unsupported transport network %q", network)
	}
	host, portText, err := net.SplitHostPort(address)
	var port uint64
	if err == nil {
		port, err = strconv.ParseUint(portText, 10, 16)
		if err != nil || port == 0 {
			return netip.Addr{}, 0, 0, fmt.Errorf("invalid numeric transport port %q", portText)
		}
	} else {
		host = address
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.IsUnspecified() {
		return netip.Addr{}, 0, 0, fmt.Errorf("transport destination is not a numeric address: %q", address)
	}
	ip = ip.Unmap()
	if ip.Is4() != (network == "tcp4" || network == "udp4") {
		return netip.Addr{}, 0, 0, fmt.Errorf("transport family mismatch: %s %s", network, address)
	}
	return ip, uint16(port), protocol, nil
}
func (g *WindowsNetworkGuard) Authorize(ctx context.Context, generation uint64, network, address string) error {
	ip, port, protocol, err := windowsTransportDestination(network, address)
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err = ctx.Err(); err != nil {
		return err
	}
	if g.engine == 0 || generation == 0 || generation <= g.retiredThrough {
		return errors.New("Windows transport owner is retired")
	}
	key := network + "/" + ip.String() + "/" + strconv.Itoa(int(port))
	entries := g.attempts[generation]
	if _, ok := entries[key]; ok {
		return nil
	}
	c := &wfpConditions{}
	layer := c.remote(ip)
	c.scalar(wfpConditionIPProtocol, wfpUint8, uintptr(protocol))
	c.blob(wfpConditionAppID, wfpByteBlobType, g.appID)
	// Some native callbacks provide a numeric upstream inventory without a port.
	// Such a permission remains restricted to that IP, protocol, backend and
	// attempt; Go/Xray dialers always supply the actual destination port.
	if port != 0 {
		c.scalar(wfpConditionRemotePort, wfpUint16, uintptr(port))
	}
	id, err := g.add(fmt.Sprintf("Transport attempt %d %s", generation, key), layer, wfpActionPermit, 15, c)
	if err != nil {
		return err
	}
	if entries == nil {
		entries = make(map[string]uint64)
		g.attempts[generation] = entries
	}
	entries[key] = id
	log.Debug(Category, "Windows transport destination authorized", map[string]any{"generation": generation, "destination": key, "filter_id": id})
	return ctx.Err()
}
func (g *WindowsNetworkGuard) Retire(ctx context.Context, generation uint64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if generation > g.retiredThrough {
		g.retiredThrough = generation
	}
	var ids []uint64
	for _, id := range g.attempts[generation] {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	if g.engine == 0 {
		return errors.New("Windows protection closed before attempt retirement")
	}
	if err := g.transaction(ctx, func() error { return g.deleteIDs(ids) }); err != nil {
		return err
	}
	delete(g.attempts, generation)
	log.Debug(Category, "Windows transport permissions retired", map[string]any{"generation": generation, "filter_ids": ids})
	return nil
}

// UpdateBootstrap allows only this backend to query the captured physical DNS
// servers. The Windows DNS client chooses caching and DNS64 behavior. No global
// resolver or public-server fallback is admitted outside the tunnel.
func (g *WindowsNetworkGuard) UpdateBootstrap(ctx context.Context, identity string, servers []netip.Addr) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if g.engine == 0 {
		return errors.New("Windows network protection closed")
	}
	if identity == g.bootstrapIdentity {
		return nil
	}
	var replacement []uint64
	err := g.transaction(ctx, func() error {
		if err := g.deleteIDs(g.bootstrapIDs); err != nil {
			return err
		}
		for _, ip := range servers {
			if !ip.IsValid() || ip.IsUnspecified() || ip.IsLoopback() {
				return fmt.Errorf("invalid physical DNS address %s", ip)
			}
			for _, protocol := range []uint8{wfpProtocolTCP, wfpProtocolUDP} {
				c := &wfpConditions{}
				layer := c.remote(ip)
				c.blob(wfpConditionAppID, wfpByteBlobType, g.appID)
				c.scalar(wfpConditionIPProtocol, wfpUint8, uintptr(protocol))
				c.scalar(wfpConditionRemotePort, wfpUint16, wfpDNSPort)
				id, err := g.add("Underlying resolver "+ip.String(), layer, wfpActionPermit, 15, c)
				if err != nil {
					return err
				}
				replacement = append(replacement, id)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	g.bootstrapIdentity, g.bootstrapIDs = identity, replacement
	log.Info(Category, "Windows underlying resolver protection refreshed", map[string]any{"network_identity": identity, "servers": servers, "filter_ids": replacement})
	return nil
}
func (g *WindowsNetworkGuard) failInstall(cause error) (*WindowsNetworkGuard, error) {
	if err := g.Close(); err != nil {
		return g, errors.Join(cause, err)
	}
	return nil, cause
}
func (g *WindowsNetworkGuard) Close() error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.engine == 0 {
		return nil
	}
	if _, err := windowsDNSWFPCall(windowsDNSWFP.engineClose, "FwpmEngineClose0", uintptr(g.engine)); err != nil {
		log.Error(Category, "Windows network protection removal failed", map[string]any{"sublayer_id": g.sublayerKey.String(), "cause": err.Error()})
		return err
	}
	g.engine = 0
	log.Info(Category, "Windows network protection removed", map[string]any{"sublayer_id": g.sublayerKey.String()})
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
