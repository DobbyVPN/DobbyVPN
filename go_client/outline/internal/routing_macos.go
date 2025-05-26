//go:build darwin
// +build darwin

package internal

import (
	"fmt"
	"io/ioutil"
	"os/exec"
	"path/filepath"
)

const wireguardSystemConfigPathMacOS = "/opt/homebrew/etc/wireguard/"

func executeCommand(command string) (string, error) {
	cmd := exec.Command("bash", "-c", command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("command execution failed: %w, output: %s", err, output)
	}
	logging.Info.Printf("Outline/routing: Command executed: %s, output: %s", command, output)
	return string(output), nil
}

func saveWireguardConf(config string, fileName string) error {
	systemConfigPath := filepath.Join(wireguardSystemConfigPathMacOS, fileName+".conf")
	err := ioutil.WriteFile(systemConfigPath, []byte(config), 0644)
	if err != nil {
		return fmt.Errorf("failed to save wireguard config: %w", err)
	}
	logging.Info.Printf("WireGuard config saved to %s\n", systemConfigPath)
	return nil
}

func StartTunnel(name string) {
	cmd := exec.Command("sudo", "wg-quick", "up", name)
	err := cmd.Run()

	if err != nil {
		logging.Info.Printf("Error launching tunnel %s: %v\n", name, err)
	} else {
		logging.Info.Printf("Tunnel launched: %s\n", name)
	}
}

func StopTunnel(name string) {
	cmd := exec.Command("sudo", "wg-quick", "down", name)
	err := cmd.Run()

	if err != nil {
		logging.Info.Printf("Error stopping tunnel %s: %v\n", name, err)
	} else {
		logging.Info.Printf("Tunnel stopped: %s\n", name)
	}
}

func CheckAndInstallWireGuard() error {
	cmd := exec.Command("wg", "--version")
	err := cmd.Run()

	if err != nil {
		logging.Info.Printf("WireGuard is not installed. Installing...")

		output, installErr := executeCommand("arch -arm64 brew install wireguard-tools")
		if installErr != nil {
			logging.Info.Printf("error installing WireGuard: %w, output: %s", installErr, output)
		}

		logging.Info.Printf("WireGuard successfully installed. Output: %s", output)
	} else {
		logging.Info.Printf("WireGuard is already installed.")
	}

	return nil
}

func startRouting(proxyIP string, gatewayIP string, tunName string) error {
	removeOldDefaultRoute := fmt.Sprintf("sudo route delete default")
	if _, err := executeCommand(removeOldDefaultRoute); err != nil {
		logging.Info.Printf("failed to remove old default route: %w", err)
	}

	addNewDefaultRoute := fmt.Sprintf("sudo route add default -interface %s", tunName)
	if _, err := executeCommand(addNewDefaultRoute); err != nil {
		logging.Info.Printf("failed to add new default route: %w", err)
	}

	addSpecificRoute := fmt.Sprintf("sudo route add -net %s/32 %s", proxyIP, gatewayIP)
	if _, err := executeCommand(addSpecificRoute); err != nil {
		logging.Info.Printf("failed to add specific route: %w", err)
	}

	return nil
}

func stopRouting(proxyIP string, gatewayIP string) error {
	removeNewDefaultRoute := fmt.Sprintf("sudo route delete default")
	if _, err := executeCommand(removeNewDefaultRoute); err != nil {
		logging.Info.Printf("failed to remove new default route: %w", err)
	}

	addOldDefaultRoute := fmt.Sprintf("sudo route add default %s", gatewayIP)
	if _, err := executeCommand(addOldDefaultRoute); err != nil {
		logging.Info.Printf("failed to add old default route: %w", err)
	}

	removeSpecificRoute := fmt.Sprintf("sudo route delete %s", proxyIP)
	if _, err := executeCommand(removeSpecificRoute); err != nil {
		logging.Info.Printf("failed to remove specific route: %w", err)
	}

	return nil
}
