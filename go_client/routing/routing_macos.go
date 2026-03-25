//go:build darwin
// +build darwin

package routing

import (
	"fmt"
	"go_client/log"
	"os/exec"
)

func ExecuteCommand(command string) (string, error) {
	cmd := exec.Command("bash", "-c", command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("command execution failed: %w, output: %s", err, output)
	}
	log.Infof("Outline/routing: Command executed: %s, output: %s", log.MaskStr(command), output)
	return string(output), nil
}

func run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	outStr := string(output)

	log.Infof(
		"[Routing] Command: %s %v, output: %s",
		name,
		args,
		outStr,
	)

	if err != nil {
		return outStr, fmt.Errorf("command failed: %w (%s)", err, outStr)
	}

	return outStr, nil
}

func AddProxyRoute(proxyIP string, gatewayIP string) error {
	_, err := run("route", "-n", "add", "-host", proxyIP, gatewayIP)
	if err != nil {
		log.Infof("proxy route may already exist: %v", err)
	}
	return nil
}

func StartRouting(proxyIP string, gatewayIP string, tunName string) error {

	log.Infof("[Routing] Starting. tun=%s", tunName)

	// удаляем default (не критично если нет)
	_, _ = run("route", "-n", "delete", "default")

	// ✅ КЛЮЧЕВОЙ FIX
	if _, err := run("route", "-n", "add", "default", "-interface", tunName); err != nil {
		return fmt.Errorf("failed to set default via %s: %w", tunName, err)
	}

	// маршрут до сервера мимо VPN
	if _, err := run("route", "-n", "add", "-host", proxyIP, gatewayIP); err != nil {
		log.Infof("proxy route may already exist: %v", err)
	}

	log.Infof("[Routing] default -> %s, proxy %s -> %s", tunName, proxyIP, gatewayIP)

	return nil
}

func StopRouting(proxyIP string, gatewayIP string) error {

	log.Infof("[Routing] Stopping")

	_, _ = run("route", "-n", "delete", "-host", proxyIP, gatewayIP)
	_, _ = run("route", "-n", "delete", "default")

	if _, err := run("route", "-n", "add", "default", gatewayIP); err != nil {
		return fmt.Errorf("failed to restore default: %w", err)
	}

	log.Infof("[Routing] restored default -> %s", gatewayIP)

	return nil
}
