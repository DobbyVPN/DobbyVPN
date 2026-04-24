//go:build linux
// +build linux

package routing

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/vishvananda/netlink"

	"go_module/log"
)

func ExecuteCommand(command string) (string, error) {
	log.Debugf(Category, "[Exec] → %s", log.MaskStr(command))

	cmd := exec.Command("bash", "-c", command)
	output, err := cmd.CombinedOutput()
	outStr := string(output)

	if err != nil {
		log.Errorf(Category, "[Exec][ERROR] cmd=%s err=%v output=%s",
			log.MaskStr(command), err, outStr)
		return outStr, fmt.Errorf("command execution failed: %w, output: %s", err, outStr)
	}

	log.Debugf(Category, "[Exec][OK] cmd=%s output=%s",
		log.MaskStr(command), outStr)
	return outStr, nil
}

func GetDefaultInterfaceNameLinux(gatewayIP string) (string, error) {
	log.Debugf(Category, "[Detect] Looking for default interface via gateway=%s", gatewayIP)

	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		log.Errorf(Category, "[Detect][ERROR] RouteList failed: %v", err)
		return "", fmt.Errorf("failed to list routes: %w", err)
	}

	for _, r := range routes {
		if r.Dst == nil && r.Gw != nil {
			log.Debugf(Category, "[Detect] Candidate route: gw=%s linkIndex=%d",
				r.Gw.String(), r.LinkIndex)
		}

		if r.Dst == nil && r.Gw != nil && r.Gw.String() == gatewayIP {
			var link netlink.Link
			link, err = netlink.LinkByIndex(r.LinkIndex)
			if err != nil {
				log.Errorf(Category, "[Detect][ERROR] LinkByIndex(%d) failed: %v", r.LinkIndex, err)
				return "", fmt.Errorf("failed to get link by index %d: %w", r.LinkIndex, err)
			}

			iface := link.Attrs().Name
			log.Debugf(Category, "[Detect][OK] Found interface=%s for gateway=%s", iface, gatewayIP)
			return iface, nil
		}
	}

	err = fmt.Errorf("default interface for gateway %s not found", gatewayIP)
	log.Errorf(Category, "[Detect][ERROR] %v", err)
	return "", err
}

func AddProxyRoute(proxyIP, gatewayIP, iface string) error {
	log.Debugf(Category, "[ProxyRoute] Adding route: %s/32 via %s dev %s",
		proxyIP, gatewayIP, iface)

	cmd := fmt.Sprintf("ip route replace %s/32 via %s dev %s", proxyIP, gatewayIP, iface)
	if _, err := ExecuteCommand(cmd); err != nil {
		return fmt.Errorf("failed to add proxy route: %w", err)
	}

	log.Debugf(Category, "[ProxyRoute][OK] Route installed for %s", proxyIP)
	return nil
}

func SetupMarkedRouting(tableID, priority int, iface, gatewayIP string) error {
	log.Debugf(Category, "[Mark] Setup fwmark routing: table=%d priority=%d iface=%s gateway=%s",
		tableID, priority, iface, gatewayIP)

	// route table
	log.Debugf(Category, "[Mark] Adding default route to table %d", tableID)
	if _, err := ExecuteCommand(
		fmt.Sprintf("ip route replace table %d default via %s dev %s", tableID, gatewayIP, iface),
	); err != nil {
		return fmt.Errorf("failed to add default route to table %d: %w", tableID, err)
	}

	// rule
	log.Debugf(Category, "[Mark] Installing ip rule: fwmark=%d → table=%d priority=%d",
		tableID, tableID, priority)

	_, _ = ExecuteCommand(fmt.Sprintf("ip rule del fwmark %d lookup %d priority %d", tableID, tableID, priority))

	if _, err := ExecuteCommand(
		fmt.Sprintf("ip rule add fwmark %d lookup %d priority %d", tableID, tableID, priority),
	); err != nil {
		return fmt.Errorf("failed to add fwmark rule: %w", err)
	}

	log.Debugf(Category, "[Mark][OK] fwmark routing configured")

	return nil
}

func CleanupMarkedRouting(tableID, priority int, iface, gatewayIP string) error {
	log.Debugf(Category, "[Mark][Cleanup] Removing fwmark routing (table=%d priority=%d)", tableID, priority)

	var errs []string

	if _, err := ExecuteCommand(fmt.Sprintf("ip rule del fwmark %d lookup %d priority %d", tableID, tableID, priority)); err != nil {
		errs = append(errs, err.Error())
	}

	if _, err := ExecuteCommand(fmt.Sprintf("ip route del table %d default via %s dev %s", tableID, gatewayIP, iface)); err != nil {
		errs = append(errs, err.Error())
	}

	if len(errs) > 0 {
		log.Warnf(Category, "[Mark][Cleanup][WARN] %s", strings.Join(errs, "; "))
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}

	log.Debugf(Category, "[Mark][Cleanup][OK] Cleaned")
	return nil
}

func StartRouting(proxyIP, gatewayIP, uplinkIface, tunName string) error {
	log.Debugf(Category, "[Start] Switching default route → TUN (%s)", tunName)

	log.Debugf(Category, "[Start] Removing old default route")
	_, _ = ExecuteCommand("ip route del default")

	log.Debugf(Category, "[Start] Setting default → dev %s", tunName)
	if _, err := ExecuteCommand(fmt.Sprintf("ip route replace default dev %s", tunName)); err != nil {
		return fmt.Errorf("failed to set default via tun %s: %w", tunName, err)
	}

	log.Debugf(Category, "[Start] Ensuring VPN server bypass: %s via %s dev %s",
		proxyIP, gatewayIP, uplinkIface)

	if _, err := ExecuteCommand(fmt.Sprintf("ip route replace %s/32 via %s dev %s", proxyIP, gatewayIP, uplinkIface)); err != nil {
		return fmt.Errorf("failed to add direct route for proxy %s: %w", proxyIP, err)
	}

	log.Infof(Category, "[Start][OK] default=VPN(%s), bypass=%s", tunName, proxyIP)

	return nil
}

func StopRouting(proxyIP, gatewayIP, uplinkIface string) error {
	log.Debugf(Category, "[Stop] Restoring system routing")

	log.Debugf(Category, "[Stop] Removing proxy route: %s", proxyIP)
	_, _ = ExecuteCommand(fmt.Sprintf("ip route del %s/32 via %s dev %s", proxyIP, gatewayIP, uplinkIface))

	log.Debugf(Category, "[Stop] Removing VPN default route")
	_, _ = ExecuteCommand("ip route del default")

	log.Debugf(Category, "[Stop] Restoring default via %s dev %s", gatewayIP, uplinkIface)
	if _, err := ExecuteCommand(fmt.Sprintf("ip route replace default via %s dev %s", gatewayIP, uplinkIface)); err != nil {
		return fmt.Errorf("failed to restore default route via %s dev %s: %w", gatewayIP, uplinkIface, err)
	}

	log.Infof(Category, "[Stop][OK] Routing restored")

	return nil
}
