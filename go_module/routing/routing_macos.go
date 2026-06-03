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
	log.Infof("[Routing][Scoped] Adding scoped default route: default -> %s (ifscope=%s)", gatewayIP, iface)

	cmd := fmt.Sprintf("route -n add default %s -ifscope %s", gatewayIP, iface)
	out, err := ExecuteCommand(cmd)
	if err != nil {
		log.Infof("[Routing][Scoped] WARN: scoped default may already exist or failed: %v | output=%s", err, out)
	} else {
		log.Infof("[Routing][Scoped] OK: scoped default installed via %s (%s)", iface, gatewayIP)
	}
	return nil
}

func DeleteScopedDefaultRoute(iface string) {
	log.Infof("[Routing][Scoped] Removing scoped default route (ifscope=%s)", iface)

	cmd := fmt.Sprintf("route -n delete default -ifscope %s", iface)
	out, err := ExecuteCommand(cmd)
	if err != nil {
		log.Infof("[Routing][Scoped] WARN: failed to delete scoped default: %v | output=%s", err, out)
	} else {
		log.Infof("[Routing][Scoped] OK: scoped default removed for %s", iface)
	}
}

func ExecuteCommand(command string) (string, error) {
	log.Infof("[Exec] Running route command: %s", log.MaskStr(command))

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
		log.Infof("[Exec] ERROR: command failed: %v | output=%s", err, outStr)
		return outStr, fmt.Errorf("command execution failed: %w, output: %s", err, outStr)
	}

	log.Infof("[Exec] OK: output=%s", outStr)
	return outStr, nil
}

func EnsureProxyRoute(proxyIP, gatewayIP string) (bool, error) {
	if isLoopbackIP(proxyIP) {
		log.Infof("[Routing][Bypass] Skipping direct route for loopback server: %s", proxyIP)
		return false, nil
	}

	log.Infof("[Routing][Bypass] Adding direct route for proxy: %s -> %s (bypass VPN)", proxyIP, gatewayIP)

	cmd := fmt.Sprintf("route -n add -host %s %s", proxyIP, gatewayIP)
	out, err := ExecuteCommand(cmd)
	if err != nil {
		if strings.Contains(out, "File exists") || strings.Contains(err.Error(), "File exists") {
			log.Infof("[Routing][Bypass] Route already exists for %s; leaving it unchanged", proxyIP)
			return false, nil
		}
		return false, fmt.Errorf("failed to add proxy route: %w, output: %s", err, out)
	} else {
		log.Infof("[Routing][Bypass] OK: proxy route installed")
	}

	return true, nil
}

func DeleteProxyRoute(proxyIP, gatewayIP string) error {
	if isLoopbackIP(proxyIP) {
		log.Infof("[Routing][Bypass] Skipping direct route removal for loopback server: %s", proxyIP)
		return nil
	}

	log.Infof("[Routing][Bypass] Removing direct route for proxy: %s -> %s", proxyIP, gatewayIP)

	cmd := fmt.Sprintf("route -n delete -host %s %s", proxyIP, gatewayIP)
	out, err := ExecuteCommand(cmd)
	if err != nil {
		return fmt.Errorf("failed to remove proxy route: %w, output: %s", err, out)
	}

	log.Infof("[Routing][Bypass] OK: proxy route removed")
	return nil
}

func isLoopbackIP(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.IsLoopback()
}

func startIPv6Block() error {
	log.Infof("[Routing][IPv6] Installing IPv6 blackhole routes")

	var errs []string
	for _, subnet := range []string{"::/1", "8000::/1"} {
		cmd := fmt.Sprintf("route -n add -inet6 -net %s -interface lo0 -blackhole", subnet)
		out, err := ExecuteCommand(cmd)
		if err != nil {
			if strings.Contains(out, "File exists") || strings.Contains(err.Error(), "File exists") {
				log.Infof("[Routing][IPv6] Blackhole route already exists for %s", subnet)
				continue
			}
			errs = append(errs, fmt.Sprintf("%s: %v, output: %s", subnet, err, out))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to install IPv6 blackhole routes: %s", strings.Join(errs, "; "))
	}

	log.Infof("[Routing][IPv6][OK] IPv6 blackhole routes installed")
	return nil
}

func stopIPv6Block() error {
	log.Infof("[Routing][IPv6] Removing IPv6 blackhole routes")

	var errs []string
	for _, subnet := range []string{"::/1", "8000::/1"} {
		cmd := fmt.Sprintf("route -n delete -inet6 -net %s -interface lo0", subnet)
		out, err := ExecuteCommand(cmd)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v, output: %s", subnet, err, out))
		}
	}

	if len(errs) > 0 {
		log.Infof("[Routing][IPv6][WARN] Failed to remove some IPv6 blackhole routes: %s", strings.Join(errs, "; "))
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}

	log.Infof("[Routing][IPv6][OK] IPv6 blackhole routes removed")
	return nil
}

func StartRouting(proxyIP, gatewayIP, tunName string) error {
	log.Infof("[Routing][Start] Switching system routing to VPN (tun=%s)", tunName)

	log.Infof("[Routing][Start] Deleting existing default route (if any)")
	_, _ = ExecuteCommand("route -n delete default")

	log.Infof("[Routing][Start] Setting default route -> %s (VPN)", tunName)
	cmdDefault := fmt.Sprintf("route -n add default -interface %s", tunName)
	if _, err := ExecuteCommand(cmdDefault); err != nil {
		return fmt.Errorf("failed to set default via %s: %w", tunName, err)
	}

	if err := startIPv6Block(); err != nil {
		return err
	}

	if isLoopbackIP(proxyIP) {
		log.Infof("[Routing][Start] Skipping proxy bypass route for loopback server: %s", proxyIP)
	} else {
		log.Infof("[Routing][Start] Ensuring VPN server bypass route: %s -> %s", proxyIP, gatewayIP)
		cmdProxy := fmt.Sprintf("route -n add -host %s %s", proxyIP, gatewayIP)
		if _, err := ExecuteCommand(cmdProxy); err != nil {
			log.Infof("[Routing][Start] WARN: proxy route may already exist: %v", err)
		}
	}

	log.Infof("[Routing][Start] OK: default=VPN(%s), proxy bypass=%s->%s", tunName, proxyIP, gatewayIP)

	return nil
}

func StopRouting(proxyIP, gatewayIP string) error {
	log.Infof("[Routing][Stop] Restoring system routing")

	if err := stopIPv6Block(); err != nil {
		log.Infof("[Routing][Stop][WARN] IPv6 block cleanup failed: %v", err)
	}

	if isLoopbackIP(proxyIP) {
		log.Infof("[Routing][Stop] Skipping proxy bypass removal for loopback server: %s", proxyIP)
	} else {
		log.Infof("[Routing][Stop] Removing proxy bypass route: %s", proxyIP)
		_ = DeleteProxyRoute(proxyIP, gatewayIP)
	}

	log.Infof("[Routing][Stop] Removing VPN default route")
	_, _ = ExecuteCommand("route -n delete default")

	log.Infof("[Routing][Stop] Restoring default route -> %s (physical network)", gatewayIP)
	cmdRestore := fmt.Sprintf("route -n add default %s", gatewayIP)
	if _, err := ExecuteCommand(cmdRestore); err != nil {
		return fmt.Errorf("failed to restore default: %w", err)
	}

	log.Infof("[Routing][Stop] OK: default route restored via %s", gatewayIP)

	return nil
}
