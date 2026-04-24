//go:build darwin
// +build darwin

package routing

import (
	"fmt"
	"os/exec"

	"go_module/log"
)

func AddScopedDefaultRoute(iface, gatewayIP string) error {
	log.SimpleDebugf(Category, "[Scoped] Adding scoped default route: default -> %s (ifscope=%s)", gatewayIP, iface)

	cmd := fmt.Sprintf("route -n add default %s -ifscope %s", gatewayIP, iface)
	out, err := ExecuteCommand(cmd)
	if err != nil {
		log.SimpleWarnf(Category, "[Scoped] WARN: scoped default may already exist or failed: %v | output=%s", err, out)
	} else {
		log.SimpleDebugf(Category, "[Scoped] OK: scoped default installed via %s (%s)", iface, gatewayIP)
	}
	return nil
}

func DeleteScopedDefaultRoute(iface string) {
	log.SimpleDebugf(Category, "[Scoped] Removing scoped default route (ifscope=%s)", iface)

	cmd := fmt.Sprintf("route -n delete default -ifscope %s", iface)
	out, err := ExecuteCommand(cmd)
	if err != nil {
		log.SimpleWarnf(Category, "[Scoped] WARN: failed to delete scoped default: %v | output=%s", err, out)
	} else {
		log.SimpleDebugf(Category, "[Scoped] OK: scoped default removed for %s", iface)
	}
}

func ExecuteCommand(command string) (string, error) {
	log.SimpleDebugf(Category, "[Exec] Running shell command: %s", log.MaskStr(command))

	cmd := exec.Command("bash", "-c", command)
	output, err := cmd.CombinedOutput()
	outStr := string(output)

	if err != nil {
		log.SimpleErrorf(Category, "[Exec] ERROR: command failed: %v | output=%s", err, outStr)
		return outStr, fmt.Errorf("command execution failed: %w, output: %s", err, outStr)
	}

	log.SimpleDebugf(Category, "[Exec] OK: output=%s", outStr)
	return outStr, nil
}

func AddProxyRoute(proxyIP string, gatewayIP string) error {
	log.SimpleDebugf(Category, "[Bypass] Adding direct route for proxy: %s -> %s (bypass VPN)", proxyIP, gatewayIP)

	cmd := fmt.Sprintf("route -n add -host %s %s", proxyIP, gatewayIP)
	out, err := ExecuteCommand(cmd)
	if err != nil {
		log.SimpleWarnf(Category, "[Bypass] WARN: route may already exist: %v | output=%s", err, out)
	} else {
		log.SimpleDebugf(Category, "[Bypass] OK: proxy route installed")
	}

	return nil
}

func StartRouting(proxyIP string, gatewayIP string, tunName string) error {
	log.SimpleDebugf(Category, "[Start] Switching system routing to VPN (tun=%s)", tunName)

	log.SimpleDebugf(Category, "[Start] Deleting existing default route (if any)")
	_, _ = ExecuteCommand("route -n delete default")

	log.SimpleDebugf(Category, "[Start] Setting default route -> %s (VPN)", tunName)
	cmdDefault := fmt.Sprintf("route -n add default -interface %s", tunName)
	if _, err := ExecuteCommand(cmdDefault); err != nil {
		return fmt.Errorf("failed to set default via %s: %w", tunName, err)
	}

	log.SimpleDebugf(Category, "[Start] Ensuring VPN server bypass route: %s -> %s", proxyIP, gatewayIP)
	cmdProxy := fmt.Sprintf("route -n add -host %s %s", proxyIP, gatewayIP)
	if _, err := ExecuteCommand(cmdProxy); err != nil {
		log.SimpleDebugf(Category, "[Start] WARN: proxy route may already exist: %v", err)
	}

	log.SimpleInfof(Category, "[Start] OK: default=VPN(%s), proxy bypass=%s->%s", tunName, proxyIP, gatewayIP)

	return nil
}

func StopRouting(proxyIP string, gatewayIP string) error {
	log.SimpleDebugf(Category, "[Stop] Restoring system routing")

	log.SimpleDebugf(Category, "[Stop] Removing proxy bypass route: %s", proxyIP)
	cmdDeleteProxy := fmt.Sprintf("route -n delete -host %s %s", proxyIP, gatewayIP)
	_, _ = ExecuteCommand(cmdDeleteProxy)

	log.SimpleDebugf(Category, "[Stop] Removing VPN default route")
	_, _ = ExecuteCommand("route -n delete default")

	log.SimpleDebugf(Category, "[Stop] Restoring default route -> %s (physical network)", gatewayIP)
	cmdRestore := fmt.Sprintf("route -n add default %s", gatewayIP)
	if _, err := ExecuteCommand(cmdRestore); err != nil {
		return fmt.Errorf("failed to restore default: %w", err)
	}

	log.SimpleInfof(Category, "[Stop] OK: default route restored via %s", gatewayIP)

	return nil
}
