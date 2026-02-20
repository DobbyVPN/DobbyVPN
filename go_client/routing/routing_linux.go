//go:build linux
// +build linux

package routing

import (
	"fmt"
	log "go_client/logger"
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

// AddProxyRoute adds a direct route to the proxy server via the real gateway
// This should be called BEFORE creating any connections to prevent routing loops
func AddProxyRoute(proxyIP, gatewayIP string) {
	if _, err := ExecuteCommand(fmt.Sprintf("sudo ip route add %s/32 via %s", proxyIP, gatewayIP)); err != nil {
		log.Infof("failed to add early route for proxyIP: %v (may already exist)", err)
	}
}

// StartRouting configures routing: default via tunnel, exception for gateway
func StartRouting(proxyIP, gatewayIP, tunName string) error {
	// Delete old default
	if _, err := ExecuteCommand("sudo ip route del default"); err != nil {
		log.Infof("failed to remove old default route: %v", err)
	}

	// Default via TUN
	if _, err := ExecuteCommand(fmt.Sprintf("sudo ip route add default dev %s", tunName)); err != nil {
		log.Infof("failed to add default via tun: %v", err)
	}

	// Route to proxyIP via local gateway
	if _, err := ExecuteCommand(fmt.Sprintf("sudo ip route add %s/32 via %s", proxyIP, gatewayIP)); err != nil {
		log.Infof("failed to add specific route for proxyIP: %v", err)
	}

	return nil
}

func StopRouting(proxyIP, gatewayIP string) {
	// Delete default via tunnel
	if _, err := ExecuteCommand("sudo ip route del default"); err != nil {
		log.Infof("failed to remove tun default route: %v", err)
	}

	// Restore default via tunnel
	if _, err := ExecuteCommand(fmt.Sprintf("sudo ip route add default via %s", gatewayIP)); err != nil {
		log.Infof("failed to add old default route: %v", err)
	}

	// Delete route to proxyIP
	if _, err := ExecuteCommand(fmt.Sprintf("sudo ip route del %s/32", proxyIP)); err != nil {
		log.Infof("failed to remove specific route for proxyIP: %v", err)
	}
}
