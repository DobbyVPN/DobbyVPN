//go:build darwin
// +build darwin

package routing

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"

	"go_module/log"
)

func AddScopedDefaultRoute(iface, gatewayIP string) error {
	log.Debugf(Category, "[Routing][Scoped] Adding scoped default route: default -> %s (ifscope=%s)", gatewayIP, iface)

	cmd := fmt.Sprintf("route -n add default %s -ifscope %s", gatewayIP, iface)
	out, err := ExecuteCommand(cmd)
	if err != nil {
		log.Debugf(Category, "[Routing][Scoped] WARN: scoped default may already exist or failed: %v | output=%s", err, out)
	} else {
		log.Debugf(Category, "[Routing][Scoped] OK: scoped default installed via %s (%s)", iface, gatewayIP)
	}
	return nil
}

func DeleteScopedDefaultRoute(iface string) {
	log.Debugf(Category, "[Routing][Scoped] Removing scoped default route (ifscope=%s)", iface)

	cmd := fmt.Sprintf("route -n delete default -ifscope %s", iface)
	out, err := ExecuteCommand(cmd)
	if err != nil {
		log.Debugf(Category, "[Routing][Scoped] WARN: failed to delete scoped default: %v | output=%s", err, out)
	} else {
		log.Debugf(Category, "[Routing][Scoped] OK: scoped default removed for %s", iface)
	}
}

func ExecuteCommand(command string) (string, error) {
	log.Debugf(Category, "[Exec] Running route command: %s", log.MaskStr(command))

	args := strings.Fields(command)
	if len(args) == 0 {
		return "", fmt.Errorf("empty command")
	}
	if args[0] != "route" {
		return "", fmt.Errorf("unsupported routing command: %s", args[0])
	}

	cmd := exec.CommandContext(context.Background(), "route", args[1:]...) // #nosec G204 command is restricted to the route binary above.
	output, err := cmd.CombinedOutput()
	outStr := string(output)

	if err != nil {
		log.Debugf(Category, "[Exec] ERROR: command failed: %v | output=%s", err, outStr)
		return outStr, fmt.Errorf("command execution failed: %w, output: %s", err, outStr)
	}

	log.Debugf(Category, "[Exec] OK: output=%s", outStr)
	return outStr, nil
}

func EnsureProxyRoute(proxyIP, gatewayIP string) (bool, error) {
	if isLoopbackIP(proxyIP) {
		log.Debugf(Category, "[Routing][Bypass] Skipping direct route for loopback server: %s", proxyIP)
		return false, nil
	}

	log.Debugf(Category, "[Routing][Bypass] Adding direct route for proxy: %s -> %s (bypass VPN)", proxyIP, gatewayIP)

	cmd := fmt.Sprintf("route -n add -host %s %s", proxyIP, gatewayIP)
	out, err := ExecuteCommand(cmd)
	if err != nil {
		if strings.Contains(out, "File exists") || strings.Contains(err.Error(), "File exists") {
			log.Debugf(Category, "[Routing][Bypass] Route already exists for %s; leaving it unchanged", proxyIP)
			return false, nil
		}
		return false, fmt.Errorf("failed to add proxy route: %w, output: %s", err, out)
	} else {
		log.Debugf(Category, "[Routing][Bypass] OK: proxy route installed")
	}

	return true, nil
}

func DeleteProxyRoute(proxyIP, gatewayIP string) error {
	if isLoopbackIP(proxyIP) {
		log.Debugf(Category, "[Routing][Bypass] Skipping direct route removal for loopback server: %s", proxyIP)
		return nil
	}

	log.Debugf(Category, "[Routing][Bypass] Removing direct route for proxy: %s -> %s", proxyIP, gatewayIP)

	cmd := fmt.Sprintf("route -n delete -host %s %s", proxyIP, gatewayIP)
	out, err := ExecuteCommand(cmd)
	if err != nil {
		return fmt.Errorf("failed to remove proxy route: %w, output: %s", err, out)
	}

	log.Debugf(Category, "[Routing][Bypass] OK: proxy route removed")
	return nil
}

func isLoopbackIP(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.IsLoopback()
}

var ipv6DefaultSubnets = []string{"::/1", "8000::/1"}

func startIPv6Block(tunName string) error {
	log.Debugf(Category, "[Routing][IPv6] Installing IPv6 sink routes via %s", tunName)

	deleteIPv6SinkRoutes(tunName, false)

	var errs []string
	for _, subnet := range ipv6DefaultSubnets {
		cmd := fmt.Sprintf("route -n add -inet6 -net %s -interface %s", subnet, tunName)
		out, err := ExecuteCommand(cmd)
		if err != nil {
			if strings.Contains(out, "File exists") || strings.Contains(err.Error(), "File exists") {
				log.Debugf(Category, "[Routing][IPv6] Sink route already exists for %s", subnet)
				continue
			}
			errs = append(errs, fmt.Sprintf("%s via %s: %v, output: %s", subnet, tunName, err, out))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to install IPv6 sink routes: %s", strings.Join(errs, "; "))
	}

	log.Debugf(Category, "[Routing][IPv6][OK] IPv6 sink routes installed via %s", tunName)
	return nil
}

func stopIPv6Block(tunName string) error {
	log.Debugf(Category, "[Routing][IPv6] Removing IPv6 sink routes")
	if tunName == "" {
		log.Debugf(Category, "[Routing][IPv6] Skipping IPv6 sink cleanup: TUN interface is not known")
		return nil
	}

	errs := deleteIPv6SinkRoutes(tunName, true)
	if len(errs) > 0 {
		log.Debugf(Category, "[Routing][IPv6][WARN] Failed to remove some IPv6 sink routes: %s", strings.Join(errs, "; "))
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}

	log.Debugf(Category, "[Routing][IPv6][OK] IPv6 sink routes removed")
	return nil
}

func deleteIPv6SinkRoutes(tunName string, collectErrors bool) []string {
	var errs []string
	for _, subnet := range ipv6DefaultSubnets {
		cmd := fmt.Sprintf("route -n delete -inet6 -net %s -interface %s", subnet, tunName)
		out, err := ExecuteCommand(cmd)
		if err != nil && collectErrors {
			errs = append(errs, fmt.Sprintf("%s: %v, output: %s", subnet, err, out))
		}

		legacyCmd := fmt.Sprintf("route -n delete -inet6 -net %s -interface lo0", subnet)
		_, _ = ExecuteCommand(legacyCmd)
	}

	return errs
}

func StartRouting(proxyIP, gatewayIP, tunName string) error {
	log.Debugf(Category, "[Routing][Start] Switching system routing to VPN (tun=%s)", tunName)

	log.Debugf(Category, "[Routing][Start] Deleting existing default route (if any)")
	_, _ = ExecuteCommand("route -n delete default")

	log.Debugf(Category, "[Routing][Start] Setting default route -> %s (VPN)", tunName)
	cmdDefault := fmt.Sprintf("route -n add default -interface %s", tunName)
	if _, err := ExecuteCommand(cmdDefault); err != nil {
		return fmt.Errorf("failed to set default via %s: %w", tunName, err)
	}

	if err := startIPv6Block(tunName); err != nil {
		return err
	}

	if isLoopbackIP(proxyIP) {
		log.Debugf(Category, "[Routing][Start] Skipping proxy bypass route for loopback server: %s", proxyIP)
	} else {
		log.Debugf(Category, "[Routing][Start] Ensuring VPN server bypass route: %s -> %s", proxyIP, gatewayIP)
		cmdProxy := fmt.Sprintf("route -n add -host %s %s", proxyIP, gatewayIP)
		if _, err := ExecuteCommand(cmdProxy); err != nil {
			log.Debugf(Category, "[Routing][Start] WARN: proxy route may already exist: %v", err)
		}
	}

	log.Debugf(Category, "[Routing][Start] OK: default=VPN(%s), proxy bypass=%s->%s", tunName, proxyIP, gatewayIP)

	return nil
}

func StopRouting(proxyIP, gatewayIP, tunName string) error {
	log.Debugf(Category, "[Routing][Stop] Restoring system routing")

	if err := stopIPv6Block(tunName); err != nil {
		log.Debugf(Category, "[Routing][Stop][WARN] IPv6 block cleanup failed: %v", err)
	}

	if isLoopbackIP(proxyIP) {
		log.Debugf(Category, "[Routing][Stop] Skipping proxy bypass removal for loopback server: %s", proxyIP)
	} else {
		log.Debugf(Category, "[Routing][Stop] Removing proxy bypass route: %s", proxyIP)
		_ = DeleteProxyRoute(proxyIP, gatewayIP)
	}

	log.Debugf(Category, "[Routing][Stop] Removing VPN default route")
	_, _ = ExecuteCommand("route -n delete default")

	log.Debugf(Category, "[Routing][Stop] Restoring default route -> %s (physical network)", gatewayIP)
	cmdRestore := fmt.Sprintf("route -n add default %s", gatewayIP)
	if _, err := ExecuteCommand(cmdRestore); err != nil {
		return fmt.Errorf("failed to restore default: %w", err)
	}

	log.Debugf(Category, "[Routing][Stop] OK: default route restored via %s", gatewayIP)

	return nil
}
