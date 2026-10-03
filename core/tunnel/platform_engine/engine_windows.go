//go:build windows && !(android || ios)

package platform_engine

import (
	"context"
	"core/common"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"
	"unsafe"

	"core/log"
	"core/routing"

	"github.com/xjasonlyu/tun2socks/v2/core/device"
	"github.com/xjasonlyu/tun2socks/v2/core/device/tun"
	"github.com/xjasonlyu/tun2socks/v2/dialer"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

type windowsAdapterState struct {
	ownedAdapterName string
	lastIface        string
	lastLUID         winipcfg.LUID
	luidKnown        bool
	prevDNS          []netip.Addr
	prevDNSStatic    bool
	dnsKnown         bool
	dnsMutated       bool
	prevDAD          uint32
	dadMutated       bool
	tunnelIPv4       netip.Prefix
	ipv4Mutated      bool
}

var adapter windowsAdapterState

const (
	windowsTunMTU             = 1200
	windowsAdapterPrefix      = "DobbyVPN-"
	windowsAdapterRandomBytes = 16
)

func startPlatformEngine(c EngineConfig) (bool, error) {
	startedAt := time.Now()
	uplinkIface := c.UplinkIface
	if err := prepareWindowsStateForStart(); err != nil {
		return false, fmt.Errorf("restore prior Windows session state: %w", err)
	}
	log.Debugf(Category, "[Engine][Windows] proxy_ready=true uplink_iface=%s", uplinkIface)
	if routing.IsTunnelInterfaceName(uplinkIface) {
		return false, fmt.Errorf("refusing to use tunnel interface %q as Windows uplink", uplinkIface)
	}
	adapterName, err := newWindowsAdapterName()
	if err != nil {
		return false, fmt.Errorf("create owned Wintun identity: %w", err)
	}
	adapter.ownedAdapterName = adapterName

	resetTun2SocksInterfaceBinding()
	accepted, err := startStack(c.ProxyAddr, func() (device.Device, error) { return tun.Open(adapterName, windowsTunMTU) })
	if err != nil {
		return accepted, err
	}

	waitStartedAt := time.Now()
	ifName, err := waitForWintun(adapterName, 5*time.Second)
	if err != nil {
		return true, err
	}
	log.Debugf(Category, "[Engine][Windows] waitForWintun OK iface=%s elapsed=%s total=%s", ifName, time.Since(waitStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	adapter.lastIface = ifName
	iface, err := net.InterfaceByName(ifName)
	if err != nil {
		return true, (fmt.Errorf("resolve interface %q: %w", ifName, err))
	}
	adapter.lastLUID, err = winipcfg.LUIDFromIndex(uint32(iface.Index))
	if err != nil {
		return true, (fmt.Errorf("resolve interface %q LUID: %w", ifName, err))
	}
	adapter.luidKnown = true

	// This is an application-owned adapter, but it can survive a crash. Never
	// overwrite a pre-existing address or try to reconstruct it from an
	// incomplete snapshot: the next start must fail loudly instead.
	existingIPv4, err := getInterfaceIPv4Prefixes(uint32(iface.Index))
	if err != nil {
		return true, err
	}
	if len(existingIPv4) != 0 {
		return true, (fmt.Errorf("owned Wintun adapter %q already has %d IPv4 address(es); refusing to overwrite existing state", ifName, len(existingIPv4)))
	}

	previousDAD, err := getInterfaceDADTransmits(adapter.lastLUID)
	if err != nil {
		return true, err
	}
	adapter.prevDAD = previousDAD
	if previousDAD != 0 {
		adapter.dadMutated = true // Restore conservatively if IP Helper reports a partial failure.
		if err := setInterfaceDADTransmits(adapter.lastLUID, 0); err != nil {
			return true, err
		}
	}

	dnsReadStartedAt := time.Now()
	adapter.prevDNS, adapter.prevDNSStatic, err = getCurrentDNS(adapter.lastLUID)
	if err != nil {
		return true, err
	}
	adapter.dnsKnown = true
	log.Debugf(Category, "[Engine][Windows] getCurrentDNS elapsed=%s total=%s", time.Since(dnsReadStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	tunCfg := common.GetNetworkConfig()

	adapter.tunnelIPv4, err = windowsTunnelIPv4Prefix(tunCfg.TunDevice)
	if err != nil {
		return true, err
	}
	if err := adapter.lastLUID.AddIPAddress(adapter.tunnelIPv4); err != nil {
		return true, (fmt.Errorf("add owned Wintun IPv4 address: %w", err))
	}
	adapter.ipv4Mutated = true
	if err := waitForPreferredIPv4(ifName, tunCfg.TunDevice, 5*time.Second); err != nil {
		return true, err
	}
	adapter.dnsMutated = true // SetDNS may have changed state before returning an error.
	if err := routing.SetWindowsDNS(adapter.lastLUID, true, []netip.Addr{netip.MustParseAddr("1.1.1.1")}); err != nil {
		return true, err
	}

	log.Debugf(Category, "[Engine][Windows] platform engine ready iface=%s elapsed=%s", ifName, time.Since(startedAt).Truncate(time.Millisecond))
	return true, nil
}
func resetTun2SocksInterfaceBinding() {
	// engine.Stop does not clear these process-global values, and Insert only
	// writes them for a non-empty Interface. Explicitly clear a binding left by
	// an older session before starting the loopback SOCKS5 relay.
	dialer.DefaultDialer.InterfaceName.Store("")
	dialer.DefaultDialer.InterfaceIndex.Store(0)
}

func stopPlatformEngine(ctx context.Context, stopDevice func()) error {
	adapterName := adapter.ownedAdapterName
	configurationErr := cleanupWindowsState()
	stopDevice()
	removalErr := waitForWindowsAdapterRemoval(ctx, adapterName)
	err := errors.Join(configurationErr, removalErr)
	if err != nil {
		log.Errorf(Category, "[Engine][Windows] platform cleanup: %v", err)
	}
	// DNS, DAD, and the tunnel address belong to this uniquely named adapter.
	// Once Windows removes it, failed per-adapter restoration no longer needs a retry.
	if removalErr == nil {
		resetWindowsState()
	}
	return err
}

func prepareWindowsStateForStart() error {
	if adapter.ownedAdapterName != "" {
		return errors.New("previous Windows adapter cleanup is pending")
	}
	return nil
}

// RecoverStaleWindowsIPv6FirewallRules runs at backend startup, before it
// accepts desktop control requests.
func RecoverStaleWindowsIPv6FirewallRules() error {
	if adapter.ownedAdapterName != "" || adapter.lastIface != "" {
		return fmt.Errorf("cannot run Windows startup recovery while adapter %q is active", adapter.ownedAdapterName)
	}
	return routing.CleanupStaleWindowsIPv6FirewallRules()
}

func cleanupWindowsState() error {
	if adapter.lastIface == "" {
		return nil
	}

	var errs []error

	log.Debugf(Category, "[Engine][Windows] Restoring DNS. static=%v DNS=%v", adapter.prevDNSStatic, adapter.prevDNS)

	if adapter.dnsMutated {
		if !adapter.dnsKnown {
			errs = append(errs, errors.New("cannot restore DNS because its previous state was not captured"))
		} else {
			err := routing.SetWindowsDNS(adapter.lastLUID, adapter.prevDNSStatic, adapter.prevDNS)
			if err != nil {
				errs = append(errs, err)
			} else {
				adapter.dnsMutated = false
				adapter.dnsKnown = false
				adapter.prevDNS = nil
				adapter.prevDNSStatic = false
			}
		}
	}
	if adapter.ipv4Mutated && adapter.luidKnown {
		if err := adapter.lastLUID.DeleteIPAddress(adapter.tunnelIPv4); err != nil {
			errs = append(errs, fmt.Errorf("remove owned Wintun IPv4 address: %w", err))
		} else {
			adapter.ipv4Mutated = false
		}
	}
	if adapter.dadMutated {
		log.Debugf(Category, "[Engine][Windows] restoring DAD transmits iface=%s count=%d", adapter.lastIface, adapter.prevDAD)
		if err := setInterfaceDADTransmits(adapter.lastLUID, adapter.prevDAD); err != nil {
			errs = append(errs, fmt.Errorf("restore DAD transmits: %w", err))
		} else {
			adapter.dadMutated = false
			adapter.prevDAD = 0
		}
	}
	return errors.Join(errs...)
}

func resetWindowsState() { adapter = windowsAdapterState{} }

func newWindowsAdapterName() (string, error) {
	random := make([]byte, windowsAdapterRandomBytes)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return windowsAdapterPrefix + hex.EncodeToString(random), nil
}

func waitForWintun(name string, timeout time.Duration) (string, error) {
	iface, err := routing.WaitForInterfaceName(name, timeout)
	if err != nil {
		return "", fmt.Errorf("owned Wintun adapter %q not found: %w", name, err)
	}
	return iface.Name, nil
}

var listWindowsInterfaces = net.Interfaces

func windowsAdapterPresent(name string) (bool, error) {
	interfaces, err := listWindowsInterfaces()
	if err != nil {
		return false, err
	}
	for _, iface := range interfaces {
		if iface.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func waitForWindowsAdapterRemoval(ctx context.Context, name string) error {
	if name == "" {
		return nil
	}
	startedAt := time.Now()
	for {
		present, err := windowsAdapterPresent(name)
		if err == nil {
			if !present {
				log.Debugf(Category, "[Engine][Windows] owned adapter removed elapsed=%s", time.Since(startedAt).Truncate(time.Millisecond))
				return nil
			}
		}
		if ctx.Err() != nil {
			if err != nil {
				return fmt.Errorf("verify owned Wintun adapter removal: %w", err)
			}
			return fmt.Errorf("owned Wintun adapter removal: %w", ctx.Err())
		}
		select {
		case <-ctx.Done():
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func platformInterfaceName() string { return adapter.lastIface }

func getInterfaceDADTransmits(luid winipcfg.LUID) (uint32, error) {
	row, err := luid.IPInterface(windows.AF_INET)
	if err != nil {
		return 0, fmt.Errorf("read DAD settings: %w", err)
	}
	return row.DadTransmits, nil
}

func setInterfaceDADTransmits(luid winipcfg.LUID, transmits uint32) error {
	row, err := luid.IPInterface(windows.AF_INET)
	if err != nil {
		return err
	}
	row.DadTransmits = transmits
	return row.Set()
}

func waitForPreferredIPv4(name, expectedAddress string, timeout time.Duration) error {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return fmt.Errorf("resolve interface %q for IPv4 readiness: %w", name, err)
	}
	expected := net.ParseIP(expectedAddress).To4()
	if expected == nil {
		return fmt.Errorf("parse expected IPv4 address for interface %q", name)
	}
	startedAt := time.Now()
	deadline := startedAt.Add(timeout)
	for {
		found, preferred, skipAsSource, duplicate, err := interfaceIPv4State(uint32(iface.Index), expected)
		if err != nil {
			return fmt.Errorf("read IPv4 readiness for interface %q: %w", name, err)
		}
		if found && preferred && !skipAsSource {
			log.Debugf(
				Category,
				"[Engine][Windows] IPv4 source ready iface=%s elapsed=%s",
				name,
				time.Since(startedAt).Truncate(time.Millisecond),
			)
			return nil
		}
		if duplicate {
			return fmt.Errorf("IPv4 address for interface %q is duplicate", name)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf(
				"IPv4 address for interface %q not preferred after %s found=%t skipAsSource=%t",
				name,
				time.Since(startedAt).Truncate(time.Millisecond),
				found,
				skipAsSource,
			)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func interfaceIPv4State(interfaceIndex uint32, expected net.IP) (found, preferred, skipAsSource, duplicate bool, err error) {
	var table *windows.MibUnicastIpAddressTable
	if err = windows.GetUnicastIpAddressTable(windows.AF_INET, &table); err != nil {
		return
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	if table.NumEntries == 0 {
		return false, false, false, false, nil
	}
	rows := unsafe.Slice(&table.Table[0], table.NumEntries)
	return findInterfaceIPv4State(rows, interfaceIndex, expected)
}

func findInterfaceIPv4State(rows []windows.MibUnicastIpAddressRow, interfaceIndex uint32, expected net.IP) (found, preferred, skipAsSource, duplicate bool, err error) {
	expected = expected.To4()
	if expected == nil {
		return false, false, false, false, fmt.Errorf("expected address is not IPv4")
	}
	for index := range rows {
		row := &rows[index]
		if row.InterfaceIndex != interfaceIndex || row.Address.Family != windows.AF_INET {
			continue
		}
		raw := (*windows.RawSockaddrInet4)(unsafe.Pointer(&row.Address))
		if !net.IP(raw.Addr[:]).Equal(expected) {
			continue
		}
		return true,
			row.DadState == windows.IpDadStatePreferred,
			row.SkipAsSource != 0,
			row.DadState == windows.IpDadStateDuplicate,
			nil
	}
	return false, false, false, false, nil
}

func windowsTunnelIPv4Prefix(address string) (netip.Prefix, error) {
	addr, err := netip.ParseAddr(address)
	if err != nil || !addr.Is4() {
		return netip.Prefix{}, fmt.Errorf("parse tunnel IPv4 address %q", address)
	}
	return netip.PrefixFrom(addr, 24), nil
}

func getCurrentDNS(luid winipcfg.LUID) ([]netip.Addr, bool, error) {
	dns, err := luid.DNS()
	if err != nil {
		return nil, false, fmt.Errorf("read current DNS: %w", err)
	}
	static, err := getInterfaceDNSStatic(luid)
	if err != nil {
		return nil, false, err
	}
	log.Debugf(Category, "[Engine][Windows] captured DNS static=%v servers=%v", static, dns)
	return dns, static, nil
}

func getInterfaceDNSStatic(luid winipcfg.LUID) (bool, error) {
	guid, err := luid.GUID()
	if err != nil {
		return false, fmt.Errorf("resolve DNS interface GUID: %w", err)
	}
	path := `SYSTEM\CurrentControlSet\Services\Tcpip\Parameters\Interfaces\` + guid.String()
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE)
	if err != nil {
		return false, fmt.Errorf("open DNS interface settings: %w", err)
	}
	defer key.Close()
	nameServers, _, err := key.GetStringValue("NameServer")
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read DNS interface origin: %w", err)
	}
	return dnsNameServerIsStatic(nameServers), nil
}

func dnsNameServerIsStatic(nameServers string) bool {
	return strings.TrimSpace(nameServers) != ""
}

func getInterfaceIPv4Prefixes(interfaceIndex uint32) ([]netip.Prefix, error) {
	var table *windows.MibUnicastIpAddressTable
	if err := windows.GetUnicastIpAddressTable(windows.AF_INET, &table); err != nil {
		return nil, fmt.Errorf("snapshot IPv4 addresses: %w", err)
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	if table.NumEntries == 0 {
		return []netip.Prefix{}, nil
	}
	rows := unsafe.Slice(&table.Table[0], table.NumEntries)
	prefixes := make([]netip.Prefix, 0)
	for index := range rows {
		row := &rows[index]
		if row.InterfaceIndex != interfaceIndex || row.Address.Family != windows.AF_INET || row.OnLinkPrefixLength > 32 {
			continue
		}
		raw := (*windows.RawSockaddrInet4)(unsafe.Pointer(&row.Address))
		prefixes = append(prefixes, netip.PrefixFrom(netip.AddrFrom4(raw.Addr), int(row.OnLinkPrefixLength)))
	}
	return prefixes, nil
}
