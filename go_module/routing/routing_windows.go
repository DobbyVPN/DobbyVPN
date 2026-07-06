//go:build windows

package routing

import (
	"bufio"
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

const ipv6BlockRuleName = "DobbyVPN Block IPv6"

var (
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

func startIPv6Block() error {
	log.Debugf(Category, "Outline/routing: Installing IPv6 outbound block rule")

	_, _ = ExecuteCommand(fmt.Sprintf("netsh advfirewall firewall delete rule name=\"%s\"", ipv6BlockRuleName))

	ipv6RemoteRanges := []string{
		"0000:0000:0000:0000:0000:0000:0000:0000-ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff",
		"0::/0",
		"::/0",
	}
	var errs []string
	for _, remoteIP := range ipv6RemoteRanges {
		command := fmt.Sprintf(
			"netsh advfirewall firewall add rule name=\"%s\" dir=out action=block enable=yes remoteip=%s",
			ipv6BlockRuleName,
			remoteIP,
		)
		if _, err := ExecuteCommand(command); err == nil {
			log.Debugf(Category, "Outline/routing: IPv6 outbound block rule installed with remoteip=%s", remoteIP)
			return nil
		} else {
			errs = append(errs, fmt.Sprintf("remoteip=%s: %v", remoteIP, err))
		}
	}

	return fmt.Errorf("failed to install IPv6 outbound block rule: %s", strings.Join(errs, "; "))
}

func stopIPv6Block() error {
	log.Debugf(Category, "Outline/routing: Removing IPv6 outbound block rule")

	command := fmt.Sprintf("netsh advfirewall firewall delete rule name=\"%s\"", ipv6BlockRuleName)
	if _, err := ExecuteCommand(command); err != nil {
		return fmt.Errorf("failed to remove IPv6 outbound block rule: %w", err)
	}

	log.Debugf(Category, "Outline/routing: IPv6 outbound block rule removed")
	return nil
}

func StartRouting(proxyIP string, GatewayIP string, TunDeviceName string, InterfaceName string, TunGateway string, TunDeviceIP string) error {
	log.Debugf(Category, "Outline/routing: Starting routing configuration for Windows...")
	log.Debugf(Category, "Outline/routing: Proxy IP: %s, Tun Device Name: %s, Tun Gateway: %s, Tun Device IP: %s, Gateway IP: %s, Interface Name: %s",
		proxyIP, TunDeviceName, TunGateway, TunDeviceIP, GatewayIP, InterfaceName)
	log.Debugf(Category, "Outline/routing: Setting up IP rule...")
	if _, err := EnsureProxyRoute(proxyIP, GatewayIP, InterfaceName); err != nil {
		return err
	}
	log.Debugf(Category, "Outline/routing: Added IP proxy rules via table")
	if err := addOrUpdateReservedSubnetBypass(GatewayIP, InterfaceName); err != nil {
		return err
	}
	log.Debugf(Category, "Outline/routing: Added IP reserved rules via table")
	if err := addIpv4TunRedirect(TunGateway, TunDeviceName); err != nil {
		return err
	}
	log.Debugf(Category, "Outline/routing: Added default IPv4 redirect routes via TUN")
	if err := startIPv6Block(); err != nil {
		log.Debugf(Category, "Outline/routing: IPv6 outbound block rule was not installed; continuing with IPv4 routing: %v", err)
	}

	log.Debugf(Category, "Outline/routing: Routing configuration completed successfully.")
	return nil
}

func StopRouting(proxyIp string, TunDeviceName string, GatewayIP string, InterfaceName string, TunGateway string) {
	log.Debugf(Category, "Outline/routing: Cleaning up routing table and rules...")
	if err := stopIPv6Block(); err != nil {
		log.Debugf(Category, "Outline/routing: Failed to remove IPv6 outbound block rule: %v", err)
	}
	if err := DeleteProxyRoute(proxyIp, GatewayIP, InterfaceName); err != nil {
		log.Debugf(Category, "Outline/routing: Failed to delete proxy route for IP %s: %v", proxyIp, err)
	}
	if err := removeReservedSubnetBypass(); err != nil {
		log.Debugf(Category, "Outline/routing: Failed to remove reserved subnet bypass routes: %v", err)
	}
	if err := stopRoutingIpv4(TunDeviceName); err != nil {
		log.Debugf(Category, "Outline/routing: Failed to remove IPv4 TUN redirect routes: %v", err)
	}
	log.Debugf(Category, "Outline/routing: Cleaned up routing table and rules.")
}

func EnsureProxyRoute(proxyIp string, gatewayIp string, interfaceName string) (bool, error) {
	if isLoopbackIP(proxyIp) {
		log.Debugf(Category, "Outline/routing: Skipping proxy route for loopback server: %s", proxyIp)
		return false, nil
	}

	// Try updating an existing route first (locale-independent duplicate handling)
	if _, err := executeNetshCommand(
		"interface", "ipv4", "set", "route", proxyIp+"/32",
		"nexthop="+gatewayIp,
		"interface="+interfaceName,
		"metric=0",
		"store=active",
	); err == nil {
		log.Debugf(Category, "Outline/routing: Proxy route already exists for IP %s; leaving it unchanged", proxyIp)
		return false, nil
	}

	if _, err := executeNetshCommand(
		"interface", "ipv4", "add", "route", proxyIp+"/32",
		"nexthop="+gatewayIp,
		"interface="+interfaceName,
		"metric=0",
		"store=active",
	); err != nil {
		return false, fmt.Errorf("failed to add proxy route for IP %s: %w", proxyIp, err)
	}
	return true, nil
}

func isLoopbackIP(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.IsLoopback()
}

func DeleteProxyRoute(proxyIp string, GatewayIP string, InterfaceName string) error {
	if isLoopbackIP(proxyIp) {
		log.Debugf(Category, "Outline/routing: Skipping proxy route removal for loopback server: %s", proxyIp)
		return nil
	}

	command := fmt.Sprintf("netsh interface ipv4 delete route %s/32 \"%s\" %s", proxyIp, InterfaceName, GatewayIP)
	_, err := ExecuteCommand(command)
	if err != nil {
		return fmt.Errorf("failed to delete proxy route for IP %s: %w", proxyIp, err)
	}
	return nil
}

func addOrUpdateReservedSubnetBypass(gatewayIp string, interfaceName string) error {
	var errs []string
	for _, subnet := range ipv4ReservedSubnets {
		// Use netsh directly since it supports interface names
		netshCommand := fmt.Sprintf("netsh interface ipv4 set route %s nexthop=%s interface=\"%s\" metric=0 store=active",
			subnet, gatewayIp, interfaceName)
		_, err := ExecuteCommand(netshCommand)
		if err != nil {
			// Route might not exist yet, try add
			addCommand := fmt.Sprintf("netsh interface ipv4 add route %s nexthop=%s interface=\"%s\" metric=0 store=active",
				subnet, gatewayIp, interfaceName)
			_, err = ExecuteCommand(addCommand)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", subnet, err))
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("failed to add or update reserved subnet bypass routes: %s", strings.Join(errs, "; "))
	}
	return nil
}

func removeReservedSubnetBypass() error {
	var errs []string
	for _, subnet := range ipv4ReservedSubnets {
		command := fmt.Sprintf("route delete %s", subnet)
		_, err := ExecuteCommand(command)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", subnet, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func addIpv4TunRedirect(tunGatewayIP string, tunDeviceName string) error {
	var errs []string
	for _, subnet := range ipv4Subnets {
		command := fmt.Sprintf("netsh interface ipv4 add route %s nexthop=%s interface=\"%s\" metric=0 store=active",
			subnet, tunGatewayIP, tunDeviceName)
		_, err := ExecuteCommand(command)
		if err != nil {
			setCommand := fmt.Sprintf("netsh interface ipv4 set route %s nexthop=%s interface=\"%s\" metric=0 store=active",
				subnet, tunGatewayIP, tunDeviceName)
			_, err = ExecuteCommand(setCommand)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", subnet, err))
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("failed to add or set TUN redirect routes: %s", strings.Join(errs, "; "))
	}
	return nil
}

func stopRoutingIpv4(tunDeviceName string) error {
	var errs []string
	for _, subnet := range ipv4Subnets {
		command := fmt.Sprintf("netsh interface ipv4 delete route %s interface=\"%s\" store=active", subnet, tunDeviceName)
		_, err := ExecuteCommand(command)
		if err != nil {
			// Fallback: try route delete
			fallbackCmd := fmt.Sprintf("route delete %s", subnet)
			_, err = ExecuteCommand(fallbackCmd)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", subnet, err))
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
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

func WaitForInterfaceNameContains(namePart string, timeout time.Duration) (*net.Interface, error) {
	label := fmt.Sprintf("interface name containing %q", namePart)
	needle := strings.ToLower(namePart)
	return waitForInterfaceChange(timeout, label, func() (*net.Interface, error) {
		interfaces, err := net.Interfaces()
		if err != nil {
			return nil, err
		}
		for _, ifc := range interfaces {
			if strings.Contains(strings.ToLower(ifc.Name), needle) {
				return &ifc, nil
			}
		}
		return nil, fmt.Errorf("%s is not present", label)
	})
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
