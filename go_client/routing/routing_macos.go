//go:build darwin
// +build darwin

package routing

import (
	"fmt"
	"go_client/log"
	"os/exec"
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
	log.Infof("[Exec] Running shell command: %s", log.MaskStr(command))

	cmd := exec.Command("bash", "-c", command)
	output, err := cmd.CombinedOutput()
	outStr := string(output)

	if err != nil {
		log.Infof("[Exec] ERROR: command failed: %v | output=%s", err, outStr)
		return outStr, fmt.Errorf("command execution failed: %w, output: %s", err, outStr)
	}

	log.Infof("[Exec] OK: output=%s", outStr)
	return outStr, nil
}

func AddProxyRoute(proxyIP string, gatewayIP string) error {
	log.Infof("[Routing][Bypass] Adding direct route for proxy: %s -> %s (bypass VPN)", proxyIP, gatewayIP)

	cmd := fmt.Sprintf("route -n add -host %s %s", proxyIP, gatewayIP)
	out, err := ExecuteCommand(cmd)
	if err != nil {
		log.Infof("[Routing][Bypass] WARN: route may already exist: %v | output=%s", err, out)
	} else {
		log.Infof("[Routing][Bypass] OK: proxy route installed")
	}

	return nil
}

func StartRouting(proxyIP string, gatewayIP string, tunName string) error {
	log.Infof("[Routing][Start] Switching system routing to VPN (tun=%s)", tunName)

	log.Infof("[Routing][Start] Deleting existing default route (if any)")
	_, _ = ExecuteCommand("route -n delete default")

	log.Infof("[Routing][Start] Setting default route -> %s (VPN)", tunName)
	cmdDefault := fmt.Sprintf("route -n add default -interface %s", tunName)
	if _, err := ExecuteCommand(cmdDefault); err != nil {
		return fmt.Errorf("failed to set default via %s: %w", tunName, err)
	}

	log.Infof("[Routing][Start] Ensuring VPN server bypass route: %s -> %s", proxyIP, gatewayIP)
	cmdProxy := fmt.Sprintf("route -n add -host %s %s", proxyIP, gatewayIP)
	if _, err := ExecuteCommand(cmdProxy); err != nil {
		log.Infof("[Routing][Start] WARN: proxy route may already exist: %v", err)
	}

	log.Infof("[Routing][Start] OK: default=VPN(%s), proxy bypass=%s->%s", tunName, proxyIP, gatewayIP)

	return nil
}

func StopRouting(proxyIP string, gatewayIP string) error {
	log.Infof("[Routing][Stop] Restoring system routing")

	log.Infof("[Routing][Stop] Removing proxy bypass route: %s", proxyIP)
	cmdDeleteProxy := fmt.Sprintf("route -n delete -host %s %s", proxyIP, gatewayIP)
	_, _ = ExecuteCommand(cmdDeleteProxy)

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
