//go:build darwin
// +build darwin

package routing

import (
	"fmt"
	log "github.com/sirupsen/logrus"
	"io/ioutil"
	"os/exec"
	"path/filepath"
)

const wireguardSystemConfigPathMacOS = "/opt/homebrew/etc/wireguard/"

func ExecuteCommand(command string) (string, error) {
	cmd := exec.Command("bash", "-c", command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("command execution failed: %w, output: %s", err, output)
	}
	log.Infof("GoClient/routing: Command executed: %s, output: %s", command, output)
	return string(output), nil
}

func saveWireguardConf(config string, fileName string) error {
	systemConfigPath := filepath.Join(wireguardSystemConfigPathMacOS, fileName+".conf")
	err := ioutil.WriteFile(systemConfigPath, []byte(config), 0644)
	if err != nil {
		return fmt.Errorf("failed to save wireguard config: %w", err)
	}
	log.Infof("WireGuard config saved to %s\n", systemConfigPath)
	return nil
}

func StartTunnel(name string) {
	cmd := exec.Command("sudo", "wg-quick", "up", name)
	err := cmd.Run()

	if err != nil {
		log.Errorf("Error launching tunnel %s: %v\n", name, err)
	} else {
		log.Infof("Tunnel launched: %s\n", name)
	}
}

func StopTunnel(name string) {
	cmd := exec.Command("sudo", "wg-quick", "down", name)
	err := cmd.Run()

	if err != nil {
		log.Errorf("Error stopping tunnel %s: %v\n", name, err)
	} else {
		log.Infof("Tunnel stopped: %s\n", name)
	}
}

func CheckAndInstallWireGuard() error {
	cmd := exec.Command("wg", "--version")
	err := cmd.Run()

	if err != nil {
		log.Infof("WireGuard is not installed. Installing...")

		output, installErr := ExecuteCommand("arch -arm64 brew install wireguard-tools")
		if installErr != nil {
			log.Errorf("error installing WireGuard: %w, output: %s", installErr, output)
		}

		log.Infof("WireGuard successfully installed. Output: %s", output)
	} else {
		log.Infof("WireGuard is already installed.")
	}

	return nil
}

func StartRouting(proxyIP string, gatewayIP string, tunName string) error {
	removeOldDefaultRoute := fmt.Sprintf("sudo route delete default")
	if _, err := ExecuteCommand(removeOldDefaultRoute); err != nil {
		log.Errorf("failed to remove old default route: %w", err)
	}

	addNewDefaultRoute := fmt.Sprintf("sudo route add default -interface %s", tunName)
	if _, err := ExecuteCommand(addNewDefaultRoute); err != nil {
		log.Errorf("failed to add new default route: %w", err)
	}

	addSpecificRoute := fmt.Sprintf("sudo route add -net %s/32 %s", proxyIP, gatewayIP)
	if _, err := ExecuteCommand(addSpecificRoute); err != nil {
		log.Errorf("failed to add specific route: %w", err)
	}

	return nil
}

func StopRouting(proxyIP string, gatewayIP string) error {
	removeNewDefaultRoute := fmt.Sprintf("sudo route delete default")
	if _, err := ExecuteCommand(removeNewDefaultRoute); err != nil {
		log.Errorf("failed to remove new default route: %w", err)
	}

	addOldDefaultRoute := fmt.Sprintf("sudo route add default %s", gatewayIP)
	if _, err := ExecuteCommand(addOldDefaultRoute); err != nil {
		log.Errorf("failed to add old default route: %w", err)
	}

	removeSpecificRoute := fmt.Sprintf("sudo route delete %s", proxyIP)
	if _, err := ExecuteCommand(removeSpecificRoute); err != nil {
		log.Errorf("failed to remove specific route: %w", err)
	}

	return nil
}
