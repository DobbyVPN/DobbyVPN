//go:build linux
// +build linux

package routing

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"

	"github.com/vishvananda/netlink"

	"go_module/log"
)

func ExecuteCommand(command string) (string, error) {
	log.Debugf(Category, "[Routing][Exec] → %s", log.MaskStr(command))

	args := strings.Fields(command)
	if len(args) == 0 {
		return "", fmt.Errorf("empty command")
	}
	if args[0] != "ip" {
		return "", fmt.Errorf("unsupported routing command: %s", args[0])
	}

	cmd := exec.CommandContext(context.Background(), "ip", args[1:]...) // #nosec G204 command is restricted to the ip binary above.
	output, err := cmd.CombinedOutput()
	outStr := string(output)

	if err != nil {
		log.Debugf(Category, "[Routing][Exec][ERROR] cmd=%s err=%v output=%s",
			log.MaskStr(command), err, outStr)
		return outStr, fmt.Errorf("command execution failed: %w, output: %s", err, outStr)
	}

	log.Debugf(Category, "[Routing][Exec][OK] cmd=%s output=%s",
		log.MaskStr(command), outStr)
	return outStr, nil
}

func GetDefaultInterfaceNameLinux(gatewayIP string) (string, error) {
	log.Debugf(Category, "[Routing][Detect] Looking for default interface via gateway=%s", gatewayIP)

	gateway := net.ParseIP(gatewayIP).To4()
	if gateway == nil {
		err := fmt.Errorf("invalid IPv4 gateway %q", gatewayIP)
		log.Debugf(Category, "[Routing][Detect][ERROR] %v", err)
		return "", err
	}

	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		log.Debugf(Category, "[Routing][Detect][ERROR] RouteList failed: %v", err)
		return "", fmt.Errorf("failed to list routes: %w", err)
	}

	for _, r := range routes {
		if r.Dst == nil && r.Gw != nil {
			log.Debugf(Category, "[Routing][Detect] Candidate route: gw=%s linkIndex=%d",
				r.Gw.String(), r.LinkIndex)
		}

		if r.Dst == nil && r.Gw != nil && r.Gw.To4() != nil && r.Gw.Equal(gateway) {
			var link netlink.Link
			link, err = netlink.LinkByIndex(r.LinkIndex)
			if err != nil {
				log.Debugf(Category, "[Routing][Detect][ERROR] LinkByIndex(%d) failed: %v", r.LinkIndex, err)
				return "", fmt.Errorf("failed to get link by index %d: %w", r.LinkIndex, err)
			}

			iface := link.Attrs().Name
			log.Debugf(Category, "[Routing][Detect][OK] Found interface=%s for gateway=%s", iface, gatewayIP)
			return iface, nil
		}
	}

	iface, procErr := getDefaultInterfaceNameFromProcRoute(gateway)
	if procErr == nil {
		log.Debugf(Category, "[Routing][Detect][OK] Found interface=%s for gateway=%s via /proc/net/route",
			iface, gatewayIP)
		return iface, nil
	}
	log.Debugf(Category, "[Routing][Detect][WARN] /proc/net/route fallback failed: %v", procErr)

	err = fmt.Errorf("default interface for gateway %s not found", gatewayIP)
	log.Debugf(Category, "[Routing][Detect][ERROR] %v", err)
	return "", err
}

func getDefaultInterfaceNameFromProcRoute(gateway net.IP) (string, error) {
	file, err := os.Open("/proc/net/route")
	if err != nil {
		return "", fmt.Errorf("failed to open /proc/net/route: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Debugf(Category, "[Routing][Detect][WARN] Failed to close /proc/net/route: %v", err)
		}
	}()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 || fields[0] == "Iface" {
			continue
		}
		if fields[1] != "00000000" {
			continue
		}

		routeGateway, err := parseProcRouteIPv4(fields[2])
		if err != nil {
			log.Debugf(Category, "[Routing][Detect][WARN] Invalid /proc/net/route gateway=%s iface=%s err=%v",
				fields[2], fields[0], err)
			continue
		}

		log.Debugf(Category, "[Routing][Detect] /proc candidate route: iface=%s gw=%s",
			fields[0], routeGateway.String())

		if routeGateway.Equal(gateway) {
			return fields[0], nil
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("failed to read /proc/net/route: %w", err)
	}

	return "", fmt.Errorf("gateway %s not found in /proc/net/route default routes", gateway.String())
}

func parseProcRouteIPv4(hexGateway string) (net.IP, error) {
	decoded, err := hex.DecodeString(hexGateway)
	if err != nil {
		return nil, err
	}
	if len(decoded) != net.IPv4len {
		return nil, fmt.Errorf("invalid IPv4 gateway length %d", len(decoded))
	}

	return net.IPv4(decoded[3], decoded[2], decoded[1], decoded[0]).To4(), nil
}

func EnsureProxyRoute(proxyIP, gatewayIP, iface string) (bool, error) {
	if isLoopbackIP(proxyIP) {
		log.Debugf(Category, "[Routing][ProxyRoute] Skipping proxy route for loopback server: %s", proxyIP)
		return false, nil
	}

	log.Debugf(Category, "[Routing][ProxyRoute] Adding route: %s/32 via %s dev %s",
		proxyIP, gatewayIP, iface)

	cmd := fmt.Sprintf("ip route add %s/32 via %s dev %s", proxyIP, gatewayIP, iface)
	if _, err := ExecuteCommand(cmd); err != nil {
		if strings.Contains(err.Error(), "File exists") {
			log.Debugf(Category, "[Routing][ProxyRoute] Route already exists for %s; leaving it unchanged", proxyIP)
			return false, nil
		}
		return false, fmt.Errorf("failed to add proxy route: %w", err)
	}

	log.Debugf(Category, "[Routing][ProxyRoute][OK] Route installed for %s", proxyIP)
	return true, nil
}

func DeleteProxyRoute(proxyIP, gatewayIP, iface string) error {
	if isLoopbackIP(proxyIP) {
		log.Debugf(Category, "[Routing][ProxyRoute] Skipping proxy route removal for loopback server: %s", proxyIP)
		return nil
	}

	log.Debugf(Category, "[Routing][ProxyRoute] Removing route: %s/32 via %s dev %s",
		proxyIP, gatewayIP, iface)

	if _, err := ExecuteCommand(fmt.Sprintf("ip route del %s/32 via %s dev %s", proxyIP, gatewayIP, iface)); err != nil {
		return fmt.Errorf("failed to delete proxy route: %w", err)
	}

	log.Debugf(Category, "[Routing][ProxyRoute][OK] Route removed for %s", proxyIP)
	return nil
}

func isLoopbackIP(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.IsLoopback()
}

func SetupMarkedRouting(tableID, priority int, iface, gatewayIP string) error {
	log.Debugf(Category, "[Routing][Mark] Setup fwmark routing: table=%d priority=%d iface=%s gateway=%s",
		tableID, priority, iface, gatewayIP)

	// route table
	log.Debugf(Category, "[Routing][Mark] Adding default route to table %d", tableID)
	if _, err := ExecuteCommand(
		fmt.Sprintf("ip route replace table %d default via %s dev %s", tableID, gatewayIP, iface),
	); err != nil {
		return fmt.Errorf("failed to add default route to table %d: %w", tableID, err)
	}

	// rule
	log.Debugf(Category, "[Routing][Mark] Installing ip rule: fwmark=%d → table=%d priority=%d",
		tableID, tableID, priority)

	_, _ = ExecuteCommand(fmt.Sprintf("ip rule del fwmark %d lookup %d priority %d", tableID, tableID, priority))

	if _, err := ExecuteCommand(
		fmt.Sprintf("ip rule add fwmark %d lookup %d priority %d", tableID, tableID, priority),
	); err != nil {
		return fmt.Errorf("failed to add fwmark rule: %w", err)
	}

	log.Debugf(Category, "[Routing][Mark][OK] fwmark routing configured")

	return nil
}

func CleanupMarkedRouting(tableID, priority int, iface, gatewayIP string) error {
	log.Debugf(Category, "[Routing][Mark][Cleanup] Removing fwmark routing (table=%d priority=%d)", tableID, priority)

	var errs []string

	if _, err := ExecuteCommand(fmt.Sprintf("ip rule del fwmark %d lookup %d priority %d", tableID, tableID, priority)); err != nil {
		errs = append(errs, err.Error())
	}

	if _, err := ExecuteCommand(fmt.Sprintf("ip route del table %d default via %s dev %s", tableID, gatewayIP, iface)); err != nil {
		errs = append(errs, err.Error())
	}

	if len(errs) > 0 {
		log.Debugf(Category, "[Routing][Mark][Cleanup][WARN] %s", strings.Join(errs, "; "))
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}

	log.Debugf(Category, "[Routing][Mark][Cleanup][OK] Cleaned")
	return nil
}

func startIPv6Block() error {
	log.Debugf(Category, "[Routing][IPv6] Installing IPv6 block routes")

	var errs []string
	for _, subnet := range []string{"::/1", "8000::/1"} {
		if _, err := ExecuteCommand(fmt.Sprintf("ip -6 route replace blackhole %s metric 1", subnet)); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", subnet, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to install IPv6 block routes: %s", strings.Join(errs, "; "))
	}

	log.Debugf(Category, "[Routing][IPv6][OK] IPv6 block routes installed")
	return nil
}

func stopIPv6Block() error {
	log.Debugf(Category, "[Routing][IPv6] Removing IPv6 block routes")

	var errs []string
	for _, subnet := range []string{"::/1", "8000::/1"} {
		if _, err := ExecuteCommand(fmt.Sprintf("ip -6 route del blackhole %s metric 1", subnet)); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", subnet, err))
		}
	}

	if len(errs) > 0 {
		log.Debugf(Category, "[Routing][IPv6][WARN] Failed to remove some IPv6 block routes: %s", strings.Join(errs, "; "))
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}

	log.Debugf(Category, "[Routing][IPv6][OK] IPv6 block routes removed")
	return nil
}

func StartRouting(proxyIP, gatewayIP, uplinkIface, tunName string) error {
	log.Debugf(Category, "[Routing][Start] Switching default route → TUN (%s)", tunName)

	log.Debugf(Category, "[Routing][Start] Removing old default route")
	_, _ = ExecuteCommand("ip route del default")

	log.Debugf(Category, "[Routing][Start] Setting default → dev %s", tunName)
	if _, err := ExecuteCommand(fmt.Sprintf("ip route replace default dev %s", tunName)); err != nil {
		return fmt.Errorf("failed to set default via tun %s: %w", tunName, err)
	}

	if err := startIPv6Block(); err != nil {
		return err
	}

	log.Debugf(Category, "[Routing][Start] Ensuring VPN server bypass: %s via %s dev %s",
		proxyIP, gatewayIP, uplinkIface)

	if isLoopbackIP(proxyIP) {
		log.Debugf(Category, "[Routing][Start] Skipping VPN server bypass route for loopback server: %s", proxyIP)
	} else {
		if _, err := ExecuteCommand(fmt.Sprintf("ip route replace %s/32 via %s dev %s", proxyIP, gatewayIP, uplinkIface)); err != nil {
			return fmt.Errorf("failed to add direct route for proxy %s: %w", proxyIP, err)
		}
	}

	log.Debugf(Category, "[Routing][Start][OK] default=VPN(%s), bypass=%s", tunName, proxyIP)

	return nil
}

func StopRouting(proxyIP, gatewayIP, uplinkIface string) error {
	log.Debugf(Category, "[Routing][Stop] Restoring system routing")

	if err := stopIPv6Block(); err != nil {
		log.Debugf(Category, "[Routing][Stop][WARN] IPv6 block cleanup failed: %v", err)
	}

	log.Debugf(Category, "[Routing][Stop] Removing proxy route: %s", proxyIP)
	if isLoopbackIP(proxyIP) {
		log.Debugf(Category, "[Routing][Stop] Skipping proxy route removal for loopback server: %s", proxyIP)
	} else {
		_ = DeleteProxyRoute(proxyIP, gatewayIP, uplinkIface)
	}

	log.Debugf(Category, "[Routing][Stop] Removing VPN default route")
	_, _ = ExecuteCommand("ip route del default")

	log.Debugf(Category, "[Routing][Stop] Restoring default via %s dev %s", gatewayIP, uplinkIface)
	if _, err := ExecuteCommand(fmt.Sprintf("ip route replace default via %s dev %s", gatewayIP, uplinkIface)); err != nil {
		return fmt.Errorf("failed to restore default route via %s dev %s: %w", gatewayIP, uplinkIface, err)
	}

	log.Debugf(Category, "[Routing][Stop][OK] Routing restored")

	return nil
}
