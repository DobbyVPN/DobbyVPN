//go:build darwin
// +build darwin

package routing

import (
	"fmt"
	"go_client/log"
	"os/exec"
)

const tunPeerIP = "169.254.19.2"

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
		"Outline/routing: Command executed: %s %v, output: %s",
		log.MaskStr(name),
		args,
		outStr,
	)

	if err != nil {
		return outStr, fmt.Errorf("command execution failed: %w, output: %s", err, outStr)
	}

	return outStr, nil
}

// AddProxyRoute adds a direct route to the proxy server via the real gateway.
// This should be called BEFORE creating any connections to prevent routing loops.
func AddProxyRoute(proxyIP string, gatewayIP string) error {
	_, err := run("route", "-n", "add", "-host", proxyIP, gatewayIP)
	if err != nil {
		log.Infof("failed to add early route for proxyIP: %v (may already exist)", err)
		return err
	}
	return nil
}

func StartRouting(proxyIP string, gatewayIP string, tunName string) error {
	// Удаляем старый default route. Если его уже нет — не падаем.
	if _, err := run("route", "-n", "delete", "default"); err != nil {
		log.Infof("failed to remove old default route (may be already removed): %v", err)
	}

	// КРИТИЧЕСКОЕ ИСПРАВЛЕНИЕ:
	// default route должен идти через peer IP utun, а не через -interface utunX
	if _, err := run("route", "-n", "add", "default", tunPeerIP); err != nil {
		return fmt.Errorf("failed to add new default route via %s: %w", tunPeerIP, err)
	}

	// Повторно гарантируем прямой маршрут до proxy/server через реальный gateway
	// Если уже существует — не считаем это фатальным.
	if _, err := run("route", "-n", "add", "-host", proxyIP, gatewayIP); err != nil {
		log.Infof("failed to add specific route (may already exist): %v", err)
	}

	log.Infof("[Routing] default -> %s, proxy %s -> %s, tun=%s", tunPeerIP, proxyIP, gatewayIP, tunName)
	return nil
}

func StopRouting(proxyIP string, gatewayIP string) error {
	// Сначала удаляем host-route до proxy/server
	if _, err := run("route", "-n", "delete", "-host", proxyIP, gatewayIP); err != nil {
		log.Infof("failed to delete specific route: %v", err)
	}

	// Удаляем default route через utun peer
	if _, err := run("route", "-n", "delete", "default"); err != nil {
		log.Infof("failed to remove new default route: %v", err)
	}

	// Возвращаем обычный default route через реальный gateway
	if _, err := run("route", "-n", "add", "default", gatewayIP); err != nil {
		return fmt.Errorf("failed to add old default route: %w", err)
	}

	log.Infof("[Routing] restored default -> %s", gatewayIP)
	return nil
}
