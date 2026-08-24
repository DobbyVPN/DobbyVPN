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

// linuxRunCommand is the shared command seam used by Linux route recovery and
// route-plan ownership tests. Production always points it at ExecuteCommand.
var linuxRunCommand = ExecuteCommand

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

// DiscoverLinuxDefaultRoute returns the physical IPv4 gateway and interface
// from the main table. During an active VPN session the ordinary default route
// may point at the TUN device, so a route without a gateway is deliberately
// ignored. This lets recovery find a newly restored uplink instead of reusing
// the stale gateway/interface captured before a link flap.
func DiscoverLinuxDefaultRoute() (string, string, error) {
	output, err := linuxRunCommand("ip -4 route show table main")
	if err != nil {
		return "", "", fmt.Errorf("discover main-table default route: %w", err)
	}
	gatewayIP, iface, parseErr := parseLinuxDefaultRoute(output)
	if parseErr != nil {
		return "", "", fmt.Errorf("%w; route output: %s", parseErr, strings.TrimSpace(output))
	}
	return gatewayIP, iface, nil
}

func parseLinuxDefaultRoute(output string) (string, string, error) {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "default" {
			continue
		}
		var gatewayIP, iface string
		for i := 1; i+1 < len(fields); i++ {
			switch fields[i] {
			case "via":
				gatewayIP = fields[i+1]
			case "dev":
				iface = fields[i+1]
			}
		}
		if net.ParseIP(gatewayIP).To4() != nil && iface != "" {
			return gatewayIP, iface, nil
		}
	}
	return "", "", fmt.Errorf("main IPv4 table has no gateway-backed default route")
}

// ReconcileLinuxSessionRoutesWithRule restores the endpoint bypass, marked
// default, and this session's fwmark rule after an uplink flap. The active
// routing plan already owns this table/priority pair, so an already-present
// rule is harmless; other kernel errors remain fatal.
func ReconcileLinuxSessionRoutesWithRule(proxyIP, gatewayIP, iface string, tableID, priority int) error {
	return reconcileLinuxSessionRoutes(proxyIP, gatewayIP, iface, tableID, priority)
}

func reconcileLinuxSessionRoutes(proxyIP, gatewayIP, iface string, tableID, priority int) error {
	if !isLoopbackIP(proxyIP) {
		if _, err := linuxRunCommand(fmt.Sprintf(
			"ip route replace %s/32 via %s dev %s proto %d metric %d",
			proxyIP, gatewayIP, iface, linuxOwnedRouteProtocol, linuxOwnedProxyMetric,
		)); err != nil {
			return fmt.Errorf("restore proxy route: %w", err)
		}
	}
	if _, err := linuxRunCommand(fmt.Sprintf(
		"ip route replace table %d default via %s dev %s proto %d",
		tableID, gatewayIP, iface, linuxOwnedRouteProtocol,
	)); err != nil {
		return fmt.Errorf("restore marked default route: %w", err)
	}
	if priority <= 0 {
		return nil
	}
	if _, err := linuxRunCommand(fmt.Sprintf("ip rule add fwmark %d lookup %d priority %d", tableID, tableID, priority)); err != nil && !strings.Contains(strings.ToLower(err.Error()), "file exists") {
		return fmt.Errorf("restore fwmark rule: %w", err)
	}
	return nil
}

func isLoopbackIP(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.IsLoopback()
}
