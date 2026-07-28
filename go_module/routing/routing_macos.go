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
	_ = iface
	_ = gatewayIP
	return fmt.Errorf("scoped default routes require a session-owned routing Plan")
}

func DeleteScopedDefaultRoute(iface string) {
	// Without the originating Plan this API cannot prove route ownership.
	log.Debugf(Category, "[Routing][Scoped] legacy cleanup skipped: route handle required interface=%s", iface)
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

var ipv6DefaultSubnets = []string{ipv6LowerHalf, ipv6UpperHalf}

func StartRouting(proxyIP, gatewayIP, tunName string) error {
	_ = proxyIP
	_ = gatewayIP
	_ = tunName
	return fmt.Errorf("legacy macOS routing start is unavailable: use a session-owned routing Plan")
}

func StopRouting(proxyIP, gatewayIP, tunName string) error {
	_ = proxyIP
	_ = gatewayIP
	_ = tunName
	return fmt.Errorf("legacy macOS routing stop is unavailable: close the originating routing Plan")
}
