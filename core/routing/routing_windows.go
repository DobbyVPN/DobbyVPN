//go:build windows

package routing

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
	"unsafe"

	"core/log"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

var ipv4Subnets = []string{
	"0.0.0.0/1",
	"128.0.0.0/1",
}

const windowsOnLinkNextHop = "0.0.0.0"

var ipv4ReservedSubnets = []string{
	"0.0.0.0/8",
	"10.0.0.0/8",
	"100.64.0.0/10",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.0.0.0/24",
	"192.0.2.0/24",
	"192.31.196.0/24",
	"192.52.193.0/24",
	"192.88.99.0/24",
	"192.168.0.0/16",
	"192.175.48.0/24",
	"198.18.0.0/15",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"240.0.0.0/4",
}

var (
	windowsRouteRead      = readWindowsRoute
	windowsRouteCreate    = (*winipcfg.MibIPforwardRow2).Create
	windowsRouteDelete    = (*winipcfg.MibIPforwardRow2).Delete
	windowsRouteIdentity  = resolveWindowsRoute
	windowsFirewallAdd    = addWindowsIPv6Rule
	windowsFirewallRemove = removeWindowsIPv6Rules

	interfaceChangeCallback = windows.NewCallback(onInterfaceChange)
	interfaceWaitersMu      sync.Mutex
	interfaceWaitersNextID  uintptr
	interfaceWaiters        = map[uintptr]chan struct{}{}
)

// A captured LUID and index prevent a reused adapter name from acquiring an
// old session's routes. Prefix and gateway are exact IP Helper identities.
type windowsRoute struct {
	prefix        string
	nextHop       string
	interfaceName string
}

func resolveWindowsRoute(route windowsRoute) (*winipcfg.MibIPforwardRow2, error) {
	iface, err := net.InterfaceByName(route.interfaceName)
	if err != nil {
		return nil, err
	}
	luid, err := winipcfg.LUIDFromIndex(uint32(iface.Index))
	if err != nil {
		return nil, err
	}
	prefix, err := netip.ParsePrefix(route.prefix)
	if err != nil {
		return nil, err
	}
	nextHop, err := netip.ParseAddr(route.nextHop)
	if err != nil {
		return nil, err
	}
	row := &winipcfg.MibIPforwardRow2{}
	row.Init()
	row.InterfaceLUID, row.InterfaceIndex = luid, uint32(iface.Index)
	if err := row.DestinationPrefix.SetPrefix(prefix.Masked()); err != nil {
		return nil, err
	}
	if err := row.NextHop.SetAddr(nextHop); err != nil {
		return nil, err
	}
	row.Metric = 0
	return row, nil
}

func readWindowsRoute(key *winipcfg.MibIPforwardRow2) (*winipcfg.MibIPforwardRow2, error) {
	row, err := key.InterfaceLUID.Route(key.DestinationPrefix.Prefix(), key.NextHop.Addr())
	if errors.Is(err, windows.ERROR_NOT_FOUND) {
		return nil, nil
	}
	return row, err
}

func sameWindowsRoute(a, b *winipcfg.MibIPforwardRow2) bool {
	return a.InterfaceLUID == b.InterfaceLUID && a.InterfaceIndex == b.InterfaceIndex &&
		a.DestinationPrefix.Prefix() == b.DestinationPrefix.Prefix() && a.NextHop.Addr() == b.NextHop.Addr() &&
		a.Metric == b.Metric && a.Protocol == b.Protocol && a.Origin == b.Origin &&
		a.Loopback == b.Loopback && a.Publish == b.Publish && a.Immortal == b.Immortal
}

// AcquireProxyRoute adds the exact VPN-server bypass route only when it is
// absent and registers its exact deletion with plan immediately.
func AcquireProxyRoute(plan *Plan, proxyIP, gatewayIP, interfaceName string) (bool, error) {
	if isLoopbackIP(proxyIP) {
		log.Debugf(Category, "Outline/routing: Skipping proxy route for loopback server: %s", proxyIP)
		return false, nil
	}
	return acquireWindowsRoute(plan, "proxy route "+proxyIP, windowsRoute{prefix: proxyIP + "/32", nextHop: gatewayIP, interfaceName: interfaceName})
}

func isLoopbackIP(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.IsLoopback()
}

func acquireWindowsRoute(plan *Plan, name string, route windowsRoute) (bool, error) {
	key, err := windowsRouteIdentity(route)
	if err != nil {
		return false, fmt.Errorf("resolve %s: %w", name, err)
	}
	existing, err := windowsRouteRead(key)
	if err != nil {
		return false, fmt.Errorf("query %s: %w", name, err)
	}
	if existing != nil {
		log.Debugf(Category, "preserving foreign route prefix=%s gateway=%s luid=%d index=%d", route.prefix, route.nextHop, key.InterfaceLUID, key.InterfaceIndex)
		return false, nil
	}
	// CreateIpForwardEntry2 is atomic. Capture its installed attributes before
	// returning; the lease remains recorded even if the verification fails.
	var installed *winipcfg.MibIPforwardRow2
	_, err = plan.Acquire(name, func() error { return windowsRouteCreate(key) }, func() error {
		if installed == nil {
			return errors.New("created route ownership could not be confirmed")
		}
		return releaseWindowsRoute(installed)
	})
	if err != nil {
		return false, err
	}
	installed, err = windowsRouteRead(key)
	if err != nil {
		return true, fmt.Errorf("capture created %s: %w", name, err)
	}
	if installed == nil {
		return true, fmt.Errorf("created %s is absent", name)
	}
	return true, nil
}

func releaseWindowsRoute(owned *winipcfg.MibIPforwardRow2) error {
	current, err := windowsRouteRead(owned)
	if err != nil {
		return err
	}
	if current == nil {
		return nil
	}
	if !sameWindowsRoute(owned, current) {
		return errors.New("session route was replaced; preserving foreign route")
	}
	deleteErr := windowsRouteDelete(current)
	remaining, err := windowsRouteRead(owned)
	if err != nil {
		return errors.Join(deleteErr, fmt.Errorf("verify route removal: %w", err))
	}
	if remaining == nil {
		return nil
	}
	return errors.Join(deleteErr, errors.New("session-owned Windows route remained after deletion"))
}

// Startup recovery recognizes only the reserved session names and expected
// outbound IPv6 block policy, using the same COM enumeration as normal cleanup.
func CleanupStaleWindowsIPv6FirewallRules() error {
	return windowsFirewallRemove(IsOwnedWindowsIPv6RuleName)
}

func acquireIPv6Block(plan *Plan) error {
	ruleName := "DobbyVPN Block IPv6 " + plan.SessionID()
	_, err := plan.Acquire("IPv6 firewall rule "+ruleName,
		func() error { return windowsFirewallAdd(ruleName) },
		func() error { return windowsFirewallRemove(func(name string) bool { return name == ruleName }) })
	return err
}

// ConfigureWindowsRouting acquires all Windows routing resources into plan.
// Any failure closes the plan, rolling back in LIFO order and leaving routes
// that existed before this session untouched.
func ConfigureWindowsRouting(plan *Plan, proxyIP, gatewayIP, tunDeviceName, interfaceName string) error {
	fail := func(err error) error {
		if rollbackErr := plan.Close(); rollbackErr != nil {
			return fmt.Errorf("%w; routing rollback: %v", err, rollbackErr)
		}
		return err
	}
	if _, err := AcquireProxyRoute(plan, proxyIP, gatewayIP, interfaceName); err != nil {
		return fail(err)
	}
	for _, subnet := range ipv4ReservedSubnets {
		if _, err := acquireWindowsRoute(plan, "reserved bypass "+subnet, windowsRoute{prefix: subnet, nextHop: gatewayIP, interfaceName: interfaceName}); err != nil {
			return fail(err)
		}
	}
	for _, subnet := range ipv4Subnets {
		// Wintun is a layer-3 adapter without a peer responding to ARP. Use an
		// explicit on-link route instead of inventing a gateway which Windows
		// can mark unreachable even though the route remains in ActiveStore.
		if _, err := acquireWindowsRoute(plan, "TUN redirect "+subnet, windowsRoute{prefix: subnet, nextHop: windowsOnLinkNextHop, interfaceName: tunDeviceName}); err != nil {
			return fail(err)
		}
	}
	if err := acquireIPv6Block(plan); err != nil {
		return fail(err)
	}
	return nil
}

// DiscoverWindowsDefaultRoute selects the live IPv4 default route by the
// combined route/interface metric. No command output or gateway dependency is used.
func DiscoverWindowsDefaultRoute() (net.IP, *net.Interface, error) {
	rows, err := winipcfg.GetIPForwardTable2(windows.AF_INET)
	if err != nil {
		return nil, nil, err
	}
	var selected *net.Interface
	var gateway net.IP
	best := uint64(^uint32(0)) * 2
	for _, row := range rows {
		if row.DestinationPrefix.Prefix() != netip.MustParsePrefix("0.0.0.0/0") || row.NextHop.Addr().IsUnspecified() {
			continue
		}
		iface, err := net.InterfaceByIndex(int(row.InterfaceIndex))
		if err != nil {
			return nil, nil, err
		}
		if iface.Flags&net.FlagUp == 0 || IsTunnelInterfaceName(iface.Name) {
			continue
		}
		settings, err := row.InterfaceLUID.IPInterface(windows.AF_INET)
		if err != nil {
			return nil, nil, err
		}
		metric := uint64(row.Metric) + uint64(settings.Metric)
		if metric < best {
			best, selected, gateway = metric, iface, net.IP(row.NextHop.Addr().AsSlice())
		}
	}
	if selected == nil {
		return nil, nil, errors.New("no active IPv4 uplink default route")
	}
	return gateway, selected, nil
}

func IsTunnelInterfaceName(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "wintun") ||
		strings.Contains(lower, "dobby") ||
		strings.Contains(lower, "wireguard") ||
		strings.Contains(lower, "tap") ||
		strings.Contains(lower, "tun")
}

func onInterfaceChange(callerContext unsafe.Pointer, _ *windows.MibIpInterfaceRow, _ uint32) uintptr {
	id := uintptr(callerContext)
	interfaceWaitersMu.Lock()
	ch := interfaceWaiters[id]
	interfaceWaitersMu.Unlock()
	if ch != nil {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	return 0
}

func nextInterfaceWaiterID() uintptr {
	interfaceWaitersMu.Lock()
	defer interfaceWaitersMu.Unlock()
	interfaceWaitersNextID++
	if interfaceWaitersNextID == 0 {
		interfaceWaitersNextID++
	}
	return interfaceWaitersNextID
}

func waitForInterfaceChange(timeout time.Duration, label string, match func() (*net.Interface, error)) (*net.Interface, error) {
	iface, err := match()
	if err == nil {
		return iface, nil
	}

	startedAt := time.Now()
	lastErr := err
	ch := make(chan struct{}, 1)
	id := nextInterfaceWaiterID()

	interfaceWaitersMu.Lock()
	interfaceWaiters[id] = ch
	interfaceWaitersMu.Unlock()
	defer func() {
		interfaceWaitersMu.Lock()
		delete(interfaceWaiters, id)
		interfaceWaitersMu.Unlock()
	}()

	var notificationHandle windows.Handle
	err = windows.NotifyIpInterfaceChange(windows.AF_UNSPEC, interfaceChangeCallback, unsafe.Pointer(id), true, &notificationHandle)
	if err != nil {
		log.Debugf(Category, "Outline/routing: interface event wait unavailable label=%s err=%v; using short fallback polling", label, err)
		iface, pollErr := waitForInterfacePolling(timeout, label, match)
		if pollErr != nil {
			return nil, errors.Join(fmt.Errorf("register interface change notification: %w", err), pollErr)
		}
		return iface, nil
	}
	defer func() {
		if notificationHandle != 0 {
			if cancelErr := windows.CancelMibChangeNotify2(notificationHandle); cancelErr != nil {
				log.Debugf(Category, "Outline/routing: interface event cancel failed label=%s err=%v", label, cancelErr)
			}
		}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-ch:
			iface, err := match()
			if err == nil {
				log.Debugf(Category, "Outline/routing: interface event wait OK label=%s iface=%s elapsed=%s", label, iface.Name, time.Since(startedAt).Truncate(time.Millisecond))
				return iface, nil
			}
			lastErr = err
		case <-timer.C:
			iface, err := match()
			if err == nil {
				log.Debugf(Category, "Outline/routing: interface event wait OK on timeout check label=%s iface=%s elapsed=%s", label, iface.Name, time.Since(startedAt).Truncate(time.Millisecond))
				return iface, nil
			}
			lastErr = err
			return nil, fmt.Errorf("%s not found after %s: %w", label, time.Since(startedAt).Truncate(time.Millisecond), lastErr)
		}
	}
}

func waitForInterfacePolling(timeout time.Duration, label string, match func() (*net.Interface, error)) (*net.Interface, error) {
	startedAt := time.Now()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		iface, err := match()
		if err == nil {
			log.Debugf(Category, "Outline/routing: interface polling wait OK label=%s iface=%s elapsed=%s", label, iface.Name, time.Since(startedAt).Truncate(time.Millisecond))
			return iface, nil
		}
		lastErr = err
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, fmt.Errorf("%s not found after %s: %w", label, time.Since(startedAt).Truncate(time.Millisecond), lastErr)
}

// WaitForInterfaceName waits for the one adapter owned by the caller. It does
// not use substring matching, which could select an unrelated Wintun adapter.
func WaitForInterfaceName(name string, timeout time.Duration) (*net.Interface, error) {
	label := fmt.Sprintf("interface named %q", name)
	return waitForInterfaceChange(timeout, label, func() (*net.Interface, error) {
		interfaces, err := net.Interfaces()
		if err != nil {
			return nil, err
		}
		return selectExactInterface(name, interfaces)
	})
}

func selectExactInterface(name string, interfaces []net.Interface) (*net.Interface, error) {
	for _, ifc := range interfaces {
		if ifc.Name == name {
			return &ifc, nil
		}
	}
	return nil, fmt.Errorf("interface named %q is not present", name)
}

func GetNetworkInterfaceByIP(currentIP string) (*net.Interface, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("error getting network interfaces: %v", err)
	}

	for _, interf := range interfaces {
		addrs, err := interf.Addrs()
		if err != nil {
			return nil, fmt.Errorf("error getting addresses for interface %v: %v", interf.Name, err)
		}

		for _, addr := range addrs {
			ip, _, parseErr := net.ParseCIDR(addr.String())
			if parseErr == nil && ip.Equal(net.ParseIP(currentIP)) {
				return &interf, nil
			}
		}
	}

	return nil, fmt.Errorf("no interface found with IP: %v", currentIP)
}

func WaitForInterfaceByIP(ip string, timeout time.Duration) (*net.Interface, error) {
	startedAt := time.Now()
	iface, err := waitForInterfaceChange(timeout, "interface with IP "+ip, func() (*net.Interface, error) {
		return GetNetworkInterfaceByIP(ip)
	})
	if err != nil {
		return nil, err
	}
	log.Debugf(Category, "Outline/routing: WaitForInterfaceByIP OK ip=%s iface=%s elapsed=%s", ip, iface.Name, time.Since(startedAt).Truncate(time.Millisecond))
	return iface, nil
}
