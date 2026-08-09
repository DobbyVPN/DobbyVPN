//go:build windows && !(android || ios)

package platform_engine

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"go_module/common"
	"net"
	"net/netip"
	"strings"
	"time"
	"unsafe"

	"go_module/log"
	"go_module/routing"

	"github.com/xjasonlyu/tun2socks/v2/dialer"
	"github.com/xjasonlyu/tun2socks/v2/engine"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

var (
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
)

const (
	windowsTunMTU                = 1200
	windowsAdapterPrefix         = "DobbyVPN-"
	windowsAdapterRandomBytes    = 16
	windowsAdapterRemovalTimeout = 60 * time.Second
)

func execAndLog(cmd string, context string) error {
	startedAt := time.Now()
	out, err := routing.ExecuteCommand(cmd)
	elapsed := time.Since(startedAt).Truncate(time.Millisecond)
	if err != nil {
		log.Debugf(Category, "[Engine][Windows][ERROR] %s elapsed=%s: %v | output=%s",
			context, elapsed, err, out,
		)
		return err
	}

	log.Debugf(Category, "[Engine][Windows][OK] %s elapsed=%s: %s", context, elapsed, out)
	return nil
}

func startPlatformEngine(cfg interface{}) error {
	startedAt := time.Now()
	c := cfg.(EngineConfig)
	uplinkIface := c.UplinkIface
	resetWindowsState()
	log.Debugf(Category, "[Engine][Windows] proxy_ready=true uplink_iface=%s", uplinkIface)
	if routing.IsTunnelInterfaceName(uplinkIface) {
		return fmt.Errorf("refusing to use tunnel interface %q as Windows uplink", uplinkIface)
	}
	adapterName, err := newWindowsAdapterName()
	if err != nil {
		return fmt.Errorf("create owned Wintun identity: %w", err)
	}
	ownedAdapterName = adapterName

	resetTun2SocksInterfaceBinding()
	key := windowsEngineKey(c, adapterName)

	engine.Insert(key)
	engineStartAt := time.Now()
	engine.Start()
	log.Debugf(Category, "[Engine][Windows] engine.Start returned elapsed=%s total=%s", time.Since(engineStartAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	waitStartedAt := time.Now()
	ifName, err := waitForWintun(adapterName, 5*time.Second)
	if err != nil {
		return failWindowsStart(err)
	}
	log.Debugf(Category, "[Engine][Windows] waitForWintun OK iface=%s elapsed=%s total=%s", ifName, time.Since(waitStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	lastIface = ifName
	iface, err := net.InterfaceByName(ifName)
	if err != nil {
		return failWindowsStart(fmt.Errorf("resolve interface %q: %w", ifName, err))
	}
	lastLUID, err = winipcfg.LUIDFromIndex(uint32(iface.Index))
	if err != nil {
		return failWindowsStart(fmt.Errorf("resolve interface %q LUID: %w", ifName, err))
	}
	luidKnown = true

	// This is an application-owned adapter, but it can survive a crash. Never
	// overwrite a pre-existing address or try to reconstruct it from an
	// incomplete snapshot: the next start must fail loudly instead.
	existingIPv4, err := getInterfaceIPv4Prefixes(uint32(iface.Index))
	if err != nil {
		return failWindowsStart(err)
	}
	if len(existingIPv4) != 0 {
		return failWindowsStart(fmt.Errorf("owned Wintun adapter %q already has %d IPv4 address(es); refusing to overwrite existing state", ifName, len(existingIPv4)))
	}

	previousDAD, err := getInterfaceDADTransmits(ifName)
	if err != nil {
		return failWindowsStart(err)
	}
	prevDAD = previousDAD
	if previousDAD != 0 {
		dadMutated = true // Restore conservatively even if netsh reports a partial failure.
		if err := setInterfaceDADTransmits(ifName, 0); err != nil {
			return failWindowsStart(err)
		}
	}

	dnsReadStartedAt := time.Now()
	prevDNS, prevDNSStatic, err = getCurrentDNS(lastLUID)
	if err != nil {
		return failWindowsStart(err)
	}
	dnsKnown = true
	log.Debugf(Category, "[Engine][Windows] getCurrentDNS elapsed=%s total=%s", time.Since(dnsReadStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	tunCfg := common.GetNetworkConfig()

	tunnelIPv4, err = windowsTunnelIPv4Prefix(tunCfg.TunDevice)
	if err != nil {
		return failWindowsStart(err)
	}
	if err := lastLUID.AddIPAddress(tunnelIPv4); err != nil {
		return failWindowsStart(fmt.Errorf("add owned Wintun IPv4 address: %w", err))
	}
	ipv4Mutated = true
	if err := waitForPreferredIPv4(ifName, tunCfg.TunDevice, 5*time.Second); err != nil {
		return failWindowsStart(err)
	}
	dnsMutated = true // SetDNS may have changed state before returning an error.
	if err := setDNS(ifName, "1.1.1.1"); err != nil {
		return failWindowsStart(err)
	}

	log.Debugf(Category, "[Engine][Windows] platform engine ready iface=%s elapsed=%s", ifName, time.Since(startedAt).Truncate(time.Millisecond))
	return nil
}

func failWindowsStart(cause error) error {
	return errors.Join(cause, stopPlatformEngine(engine.Stop))
}

func windowsEngineKey(c EngineConfig, adapterName string) *engine.Key {
	// The proxy is loopback. Binding tun2socks' process-global dialer to the
	// physical uplink also binds its address-less UDP relay socket on Windows;
	// that socket then cannot send SOCKS5 UDP-associate datagrams to loopback.
	// Real protocol sockets are protected independently by protected_dialer.
	return &engine.Key{
		Proxy:    fmt.Sprintf("socks5://%s", c.ProxyAddr),
		Device:   adapterName,
		LogLevel: "info",
		MTU:      windowsTunMTU,
	}
}

func resetTun2SocksInterfaceBinding() {
	// engine.Stop does not clear these process-global values, and Insert only
	// writes them for a non-empty Interface. Explicitly clear a binding left by
	// an older session before starting the loopback SOCKS5 relay.
	dialer.DefaultDialer.InterfaceName.Store("")
	dialer.DefaultDialer.InterfaceIndex.Store(0)
}

func stopPlatformEngine(stopDevice func()) error {
	adapterName := ownedAdapterName
	configurationErr := cleanupWindowsState()
	stopDevice()
	removalErr := waitForWindowsAdapterRemoval(adapterName, windowsAdapterRemovalTimeout)
	resetWindowsState()
	err := errors.Join(configurationErr, removalErr)
	if err != nil {
		log.Debugf(Category, "[Engine][Windows][ERROR] platform cleanup: %v", err)
	}
	return err
}

func cleanupWindowsState() error {
	if lastIface == "" {
		return nil
	}

	var errs []error

	log.Debugf(Category, "[Engine][Windows] Restoring DNS. static=%v DNS=%v", prevDNSStatic, prevDNS)

	if dnsMutated && dnsKnown && !prevDNSStatic {
		cmd := fmt.Sprintf(
			"netsh interface ipv4 set dnsservers name=\"%s\" dhcp",
			lastIface,
		)
		if err := execAndLog(cmd, "restore DNS (DHCP)"); err != nil {
			errs = append(errs, fmt.Errorf("restore DHCP DNS: %w", err))
		}
	} else if dnsMutated && dnsKnown {
		if err := restoreStaticDNS(lastIface, prevDNS); err != nil {
			errs = append(errs, err)
		}
	}
	if ipv4Mutated && luidKnown {
		if err := lastLUID.DeleteIPAddress(tunnelIPv4); err != nil {
			errs = append(errs, fmt.Errorf("remove owned Wintun IPv4 address: %w", err))
		}
	}
	if dadMutated {
		log.Debugf(Category, "[Engine][Windows] restoring DAD transmits iface=%s count=%d", lastIface, prevDAD)
		if err := setInterfaceDADTransmits(lastIface, prevDAD); err != nil {
			errs = append(errs, fmt.Errorf("restore DAD transmits: %w", err))
		}
	}
	return errors.Join(errs...)
}

func resetWindowsState() {
	ownedAdapterName = ""
	lastIface = ""
	lastLUID = 0
	luidKnown = false
	prevDNS = nil
	prevDNSStatic = false
	dnsKnown = false
	dnsMutated = false
	prevDAD = 0
	dadMutated = false
	tunnelIPv4 = netip.Prefix{}
	ipv4Mutated = false
}

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

func waitForWindowsAdapterRemoval(name string, timeout time.Duration) error {
	if name == "" {
		return nil
	}
	startedAt := time.Now()
	deadline := startedAt.Add(timeout)
	for {
		interfaces, err := listWindowsInterfaces()
		if err == nil {
			present := false
			for _, iface := range interfaces {
				if iface.Name == name {
					present = true
					break
				}
			}
			if !present {
				log.Debugf(Category, "[Engine][Windows] owned adapter removed elapsed=%s", time.Since(startedAt).Truncate(time.Millisecond))
				return nil
			}
		}
		if !time.Now().Before(deadline) {
			if err != nil {
				return fmt.Errorf("verify owned Wintun adapter removal: %w", err)
			}
			return fmt.Errorf("owned Wintun adapter was not removed within %s", timeout)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func platformInterfaceName() string { return lastIface }

func windowsSetDADCommand(name string, transmits uint32) string {
	return fmt.Sprintf(
		"netsh interface ipv4 set interface interface=\"%s\" dadtransmits=%d store=active",
		name,
		transmits,
	)
}

func getInterfaceDADTransmits(name string) (uint32, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return 0, fmt.Errorf("resolve interface %q for DAD snapshot: %w", name, err)
	}
	row := windows.MibIpInterfaceRow{
		Family:         windows.AF_INET,
		InterfaceIndex: uint32(iface.Index),
	}
	if err := windows.GetIpInterfaceEntry(&row); err != nil {
		return 0, fmt.Errorf("read interface %q DAD settings: %w", name, err)
	}
	return row.DadTransmits, nil
}

func setInterfaceDADTransmits(name string, transmits uint32) error {
	return execAndLog(windowsSetDADCommand(name, transmits), "setInterfaceDADTransmits")
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

func setDNS(name, dns string) error {
	addr, err := netip.ParseAddr(dns)
	if err != nil || !addr.Is4() {
		return fmt.Errorf("parse IPv4 DNS server %q", dns)
	}
	cmd := windowsSetDNSCommand(name, addr)
	return execAndLog(cmd, "set DNS server")
}

func windowsSetDNSCommand(name string, server netip.Addr) string {
	return fmt.Sprintf("netsh interface ipv4 set dnsservers name=\"%s\" static %s primary", name, server)
}

func windowsClearDNSCommand(name string) string {
	return fmt.Sprintf("netsh interface ipv4 delete dnsservers name=\"%s\" all", name)
}

func windowsAddDNSCommand(name string, server netip.Addr, index int) string {
	return fmt.Sprintf("netsh interface ipv4 add dnsservers name=\"%s\" %s index=%d", name, server, index)
}

func restoreStaticDNS(name string, servers []netip.Addr) error {
	if len(servers) == 0 {
		return execAndLog(windowsClearDNSCommand(name), "restore empty DNS server list")
	}
	if err := execAndLog(windowsSetDNSCommand(name, servers[0]), "restore primary DNS server"); err != nil {
		return fmt.Errorf("restore primary DNS server: %w", err)
	}
	for index, server := range servers[1:] {
		if err := execAndLog(windowsAddDNSCommand(name, server, index+2), fmt.Sprintf("restore DNS server index=%d", index+2)); err != nil {
			return fmt.Errorf("restore DNS server index=%d: %w", index+2, err)
		}
	}
	return nil
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
	ipv4DNS := make([]netip.Addr, 0, len(dns))
	for _, address := range dns {
		if address.Is4() {
			ipv4DNS = append(ipv4DNS, address)
		}
	}
	log.Debugf(Category, "[Engine][Windows] Current DNS: static=%v IPv4_DNS=%v", static, ipv4DNS)
	return ipv4DNS, static, nil
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
