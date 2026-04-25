//go:build darwin
// +build darwin

package routing

import (
	"fmt"
	"os/exec"

	"go_module/log"
)

func AddScopedDefaultRoute(iface, gatewayIP string) error {
	log.Debugf(Category, "[Scoped] Adding scoped default route: default -> %s (ifscope=%s)", gatewayIP, iface)

	cmd := fmt.Sprintf("route -n add default %s -ifscope %s", gatewayIP, iface)
	out, err := ExecuteCommand(cmd)
	if err != nil {
		log.Warnf(Category, "[Scoped] WARN: scoped default may already exist or failed: %v | output=%s", err, out)
	} else {
		log.Debugf(Category, "[Scoped] OK: scoped default installed via %s (%s)", iface, gatewayIP)
	}
	return nil
}

func DeleteScopedDefaultRoute(iface string) {
	log.Debugf(Category, "[Scoped] Removing scoped default route (ifscope=%s)", iface)

	cmd := fmt.Sprintf("route -n delete default -ifscope %s", iface)
	out, err := ExecuteCommand(cmd)
	if err != nil {
		log.Warnf(Category, "[Scoped] WARN: failed to delete scoped default: %v | output=%s", err, out)
	} else {
		log.Debugf(Category, "[Scoped] OK: scoped default removed for %s", iface)
	}
}

func ExecuteCommand(command string) (string, error) {
	log.Debugf(Category, "[Exec] Running shell command: %s", log.MaskStr(command))

	cmd := exec.Command("bash", "-c", command)
	output, err := cmd.CombinedOutput()
	outStr := string(output)

	if err != nil {
		log.Errorf(Category, "[Exec] ERROR: command failed: %v | output=%s", err, outStr)
		return outStr, fmt.Errorf("command execution failed: %w, output: %s", err, outStr)
	}

	log.Debugf(Category, "[Exec] OK: output=%s", outStr)
	return outStr, nil
}

func AddProxyRoute(proxyIP string, gatewayIP string) error {
	log.Debugf(Category, "[Bypass] Adding direct route for proxy: %s -> %s (bypass VPN)", proxyIP, gatewayIP)

	cmd := fmt.Sprintf("route -n add -host %s %s", proxyIP, gatewayIP)
	out, err := ExecuteCommand(cmd)
	if err != nil {
		log.Warnf(Category, "[Bypass] WARN: route may already exist: %v | output=%s", err, out)
	} else {
		log.Debugf(Category, "[Bypass] OK: proxy route installed")
	}

	return nil
}

func StartRouting(proxyIP string, gatewayIP string, tunName string) error {
	log.Debugf(Category, "[Start] Switching system routing to VPN (tun=%s)", tunName)

	log.Debugf(Category, "[Start] Deleting existing default route (if any)")
	_, _ = ExecuteCommand("route -n delete default")

	log.Debugf(Category, "[Start] Setting default route -> %s (VPN)", tunName)
	cmdDefault := fmt.Sprintf("route -n add default -interface %s", tunName)
	if _, err := ExecuteCommand(cmdDefault); err != nil {
		return fmt.Errorf("failed to set default via %s: %w", tunName, err)
	}

	log.Debugf(Category, "[Start] Ensuring VPN server bypass route: %s -> %s", proxyIP, gatewayIP)
	cmdProxy := fmt.Sprintf("route -n add -host %s %s", proxyIP, gatewayIP)
	if _, err := ExecuteCommand(cmdProxy); err != nil {
		log.Debugf(Category, "[Start] WARN: proxy route may already exist: %v", err)
	}

	log.Infof(Category, "[Start] OK: default=VPN(%s), proxy bypass=%s->%s", tunName, proxyIP, gatewayIP)

	return nil
}

func StopRouting(proxyIP string, gatewayIP string) error {
	log.Debugf(Category, "[Stop] Restoring system routing")

	log.Debugf(Category, "[Stop] Removing proxy bypass route: %s", proxyIP)
	cmdDeleteProxy := fmt.Sprintf("route -n delete -host %s %s", proxyIP, gatewayIP)
	_, _ = ExecuteCommand(cmdDeleteProxy)

	log.Debugf(Category, "[Stop] Removing VPN default route")
	_, _ = ExecuteCommand("route -n delete default")

	log.Debugf(Category, "[Stop] Restoring default route -> %s (physical network)", gatewayIP)
	cmdRestore := fmt.Sprintf("route -n add default %s", gatewayIP)
	if _, err := ExecuteCommand(cmdRestore); err != nil {
		return fmt.Errorf("failed to restore default: %w", err)
	}

	log.Infof(Category, "[Stop] OK: default route restored via %s", gatewayIP)

	return nil
}
