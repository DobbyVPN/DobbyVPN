//go:build darwin
// +build darwin

package routing

import (
	"fmt"
	log "go_client/logger"
	"os/exec"
)

const wireguardSystemConfigPathMacOS = "/opt/homebrew/etc/wireguard/"

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
func AddProxyRoute(proxyIP string, gatewayIP string) {
	addSpecificRoute := fmt.Sprintf("sudo route add -net %s/32 %s", proxyIP, gatewayIP)
	if _, err := ExecuteCommand(addSpecificRoute); err != nil {
		log.Infof("failed to add early route for proxyIP: %v (may already exist)", err)
	}
}

func StartRouting(proxyIP string, gatewayIP string, tunName string) error {
	removeOldDefaultRoute := fmt.Sprintf("sudo route delete default")
	if _, err := ExecuteCommand(removeOldDefaultRoute); err != nil {
		log.Infof("failed to remove old default route: %w", err)
	}

	addNewDefaultRoute := fmt.Sprintf("sudo route add default -interface %s", tunName)
	if _, err := ExecuteCommand(addNewDefaultRoute); err != nil {
		log.Infof("failed to add new default route: %w", err)
	}

	addSpecificRoute := fmt.Sprintf("sudo route add -net %s/32 %s", proxyIP, gatewayIP)
	if _, err := ExecuteCommand(addSpecificRoute); err != nil {
		log.Infof("failed to add specific route: %w", err)
	}

	return nil
}

func StopRouting(proxyIP string, gatewayIP string) error {
	addSpecificRoute := fmt.Sprintf("sudo route delete -net %s/32 %s", proxyIP, gatewayIP)
	if _, err := ExecuteCommand(addSpecificRoute); err != nil {
		log.Infof("failed to delete specific route: %w", err)
	}

	removeNewDefaultRoute := fmt.Sprintf("sudo route delete default")
	if _, err := ExecuteCommand(removeNewDefaultRoute); err != nil {
		log.Infof("failed to remove new default route: %w", err)
	}

	addOldDefaultRoute := fmt.Sprintf("sudo route add default %s", gatewayIP)
	if _, err := ExecuteCommand(addOldDefaultRoute); err != nil {
		log.Infof("failed to add old default route: %w", err)
	}

	return nil
}
