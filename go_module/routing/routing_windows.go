//go:build windows

package routing

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"go_module/log"

	"golang.org/x/sys/windows"
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
	windowsNetshCommand = executeNetshCommand
	windowsRouteExists  = routeExistsInWindowsTable

	interfaceChangeCallback = windows.NewCallback(onInterfaceChange)
	interfaceWaitersMu      sync.Mutex
	interfaceWaitersNextID  uintptr
	interfaceWaiters        = map[uintptr]chan struct{}{}
)

func ExecuteCommand(command string) (string, error) {
	startedAt := time.Now()
	cmd := exec.Command("cmd", "/C", command)

	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
	}

	output, err := cmd.CombinedOutput()
	elapsed := time.Since(startedAt).Truncate(time.Millisecond)
	if err != nil {
		return string(output), fmt.Errorf("command execution failed after %s: %w, output: %s", elapsed, err, output)
	}
	log.Debugf(Category, "Outline/routing: Command executed elapsed=%s: %s, output: %s", elapsed, log.MaskStr(command), output)
	return string(output), nil
}

func executeNetshCommand(args ...string) (string, error) {
	commandForLog := formatCommandForLog("netsh", args...)
	log.Debugf(Category, "Outline/routing: Executing command: %s", log.MaskStr(commandForLog))

	startedAt := time.Now()
	cmd := exec.Command("netsh", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
	}

	output, err := cmd.CombinedOutput()
	elapsed := time.Since(startedAt).Truncate(time.Millisecond)
	if err != nil {
		return string(output), fmt.Errorf("command execution failed after %s: %w, output: %s", elapsed, err, output)
	}
	log.Debugf(Category, "Outline/routing: Command executed elapsed=%s: %s, output: %s", elapsed, log.MaskStr(commandForLog), output)
	return string(output), nil
}

func formatCommandForLog(name string, args ...string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, name)
	for _, arg := range args {
		if strings.ContainsAny(arg, " \t\"") {
			parts = append(parts, fmt.Sprintf("%q", arg))
		} else {
			parts = append(parts, arg)
		}
	}
	return strings.Join(parts, " ")
}

// windowsRoute is the exact identity used for both the route-table lookup and
// its netsh lease.  A route owned by another process is intentionally not
// changed or deleted.
type windowsRoute struct {
	prefix        string
	nextHop       string
	interfaceName string
}

func windowsRouteArgs(action string, route windowsRoute) []string {
	args := []string{
		"interface", "ipv4", action, "route", route.prefix,
		"nexthop=" + route.nextHop,
		"interface=" + route.interfaceName,
	}
	// Windows can normalize the requested metric when it installs an
	// off-link/gateway route. Supplying metric=0 during deletion then exits
	// successfully without matching that exact route. Prefix, next hop and
	// interface are the session-owned identity; omit metric only on delete.
	if action == "add" {
		args = append(args, "metric=0")
	}
	return append(args, "store=active")
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
	exists, err := windowsRouteExists(route)
	if err != nil {
		return false, fmt.Errorf("query exact %s: %w", name, err)
	}
	if exists {
		log.Debugf(Category, "Outline/routing: preserving pre-existing %s prefix=%s nexthop=%s interface=%s", name, route.prefix, route.nextHop, route.interfaceName)
		return false, nil
	}
	if _, err := plan.Acquire(name,
		func() error {
			_, err := windowsNetshCommand(windowsRouteArgs("add", route)...)
			return err
		},
		func() error {
			return releaseWindowsRoute(route, 2*time.Second)
		},
	); err != nil {
		return false, err
	}
	return true, nil
}

func releaseWindowsRoute(route windowsRoute, timeout time.Duration) error {
	if _, err := windowsNetshCommand(windowsRouteArgs("delete", route)...); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for {
		exists, err := windowsRouteExists(route)
		if err == nil && !exists {
			return nil
		}
		if !time.Now().Before(deadline) {
			if err != nil {
				return fmt.Errorf("verify session-owned Windows route deletion: %w", err)
			}
			return errors.New("session-owned Windows route remained after deletion")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func routeExistsInWindowsTable(route windowsRoute) (bool, error) {
	iface, err := net.InterfaceByName(route.interfaceName)
	if err != nil {
		return false, fmt.Errorf("resolve interface %q: %w", route.interfaceName, err)
	}
	prefixIP, prefix, err := net.ParseCIDR(route.prefix)
	if err != nil || prefixIP.To4() == nil {
		return false, fmt.Errorf("parse IPv4 prefix %q", route.prefix)
	}
	nextHop := net.ParseIP(route.nextHop).To4()
	if nextHop == nil {
		return false, fmt.Errorf("parse IPv4 next hop %q", route.nextHop)
	}

	var table *windows.MibIpForwardTable2
	if err := windows.GetIpForwardTable2(windows.AF_INET, &table); err != nil {
		return false, err
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	for _, row := range table.Rows() {
		if row.InterfaceIndex != uint32(iface.Index) || row.DestinationPrefix.PrefixLength != uint8(prefixMaskSize(prefix)) {
			continue
		}
		if routeRowIPv4(row.DestinationPrefix.Prefix) == prefixIP.To4().String() && routeRowIPv4(row.NextHop) == nextHop.String() {
			return true, nil
		}
	}
	return false, nil
}

func prefixMaskSize(prefix *net.IPNet) int {
	ones, _ := prefix.Mask.Size()
	return ones
}

func routeRowIPv4(raw windows.RawSockaddrInet) string {
	if raw.Family != windows.AF_INET {
		return ""
	}
	addr := (*windows.RawSockaddrInet4)(unsafe.Pointer(&raw))
	return net.IP(addr.Addr[:]).String()
}

func acquireIPv6Block(plan *Plan) error {
	ruleName := "DobbyVPN Block IPv6 " + plan.SessionID()
	remoteRanges := []string{
		"0000:0000:0000:0000:0000:0000:0000:0000-ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff",
		"0::/0",
		"::/0",
	}
	_, err := plan.Acquire("IPv6 firewall rule "+ruleName, func() error {
		var errs []string
		for _, remoteIP := range remoteRanges {
			if _, err := windowsNetshCommand("advfirewall", "firewall", "add", "rule", "name="+ruleName, "dir=out", "action=block", "enable=yes", "remoteip="+remoteIP); err == nil {
				return nil
			} else {
				errs = append(errs, err.Error())
			}
		}
		return fmt.Errorf("install IPv6 block rule %q: %s", ruleName, strings.Join(errs, "; "))
	}, func() error {
		_, err := windowsNetshCommand("advfirewall", "firewall", "delete", "rule", "name="+ruleName)
		return err
	})
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

func FindInterfaceIPByGateway(gatewayIP string) (string, error) {
	cmd := exec.Command("route", "print")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
	}
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("fail to execute a command route print: %v", err)
	}

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	var foundGateway bool
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, gatewayIP) {
			foundGateway = true
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				interfaceIP := parts[3]
				iface, err := GetNetworkInterfaceByIP(interfaceIP)
				if err == nil && IsTunnelInterfaceName(iface.Name) {
					log.Debugf(Category, "Outline/routing: Skipping tunnel interface %s for gateway %s", iface.Name, gatewayIP)
					continue
				}
				return interfaceIP, nil
			}
		}
	}

	if !foundGateway {
		return "", fmt.Errorf("gateway %s is not found in the table", gatewayIP)
	}

	return "", fmt.Errorf("no interface %s", gatewayIP)
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
		return waitForInterfacePolling(timeout, label, match)
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
		case <-timer.C:
			iface, err := match()
			if err == nil {
				log.Debugf(Category, "Outline/routing: interface event wait OK on timeout check label=%s iface=%s elapsed=%s", label, iface.Name, time.Since(startedAt).Truncate(time.Millisecond))
				return iface, nil
			}
			return nil, fmt.Errorf("%s not found after %s", label, time.Since(startedAt).Truncate(time.Millisecond))
		}
	}
}

func waitForInterfacePolling(timeout time.Duration, label string, match func() (*net.Interface, error)) (*net.Interface, error) {
	startedAt := time.Now()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		iface, err := match()
		if err == nil {
			log.Debugf(Category, "Outline/routing: interface polling wait OK label=%s iface=%s elapsed=%s", label, iface.Name, time.Since(startedAt).Truncate(time.Millisecond))
			return iface, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, fmt.Errorf("%s not found after %s", label, time.Since(startedAt).Truncate(time.Millisecond))
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
			if strings.Contains(addr.String(), currentIP) {
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
