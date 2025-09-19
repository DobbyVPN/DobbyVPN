//go:build linux
// +build linux

package internal

import (
	"fmt"
	"os/exec"
)

func executeCommand(command string) (string, error) {
	cmd := exec.Command("bash", "-c", command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("command execution failed: %w, output: %s", err, output)
	}
	logging.Info.Printf("Outline/routing: Command executed: %s, output: %s", command, output)
	return string(output), nil
}

// startRouting — как на macOS: дефолт в туннель, исключения через gateway
func startRouting(proxyIP string, gatewayIP string, tunName string) error {
	// удалить старый дефолт
	if _, err := executeCommand("sudo ip route del default"); err != nil {
		logging.Info.Printf("failed to remove old default route: %v", err)
	}

	// дефолт через TUN
	if _, err := executeCommand(fmt.Sprintf("sudo ip route add default dev %s", tunName)); err != nil {
		logging.Info.Printf("failed to add default via tun: %v", err)
	}

	// маршрут к proxyIP через реальный gateway
	if _, err := executeCommand(fmt.Sprintf("sudo ip route add %s/32 via %s", proxyIP, gatewayIP)); err != nil {
		logging.Info.Printf("failed to add specific route for proxyIP: %v", err)
	}

	// маршрут к хардкод-серверу через реальный gateway
	if _, err := executeCommand(fmt.Sprintf("sudo ip route add %s/32 via %s", "85.9.223.19", gatewayIP)); err != nil {
		logging.Info.Printf("failed to add specific route for 85.9.223.19: %v", err)
	}

	return nil
}

func stopRouting(proxyIP string, gatewayIP string) {
	// удалить дефолт через туннель
	if _, err := executeCommand("sudo ip route del default"); err != nil {
		logging.Info.Printf("failed to remove tun default route: %v", err)
	}

	// восстановить дефолт через gateway
	if _, err := executeCommand(fmt.Sprintf("sudo ip route add default via %s", gatewayIP)); err != nil {
		logging.Info.Printf("failed to add old default route: %v", err)
	}

	// удалить маршрут к proxyIP
	if _, err := executeCommand(fmt.Sprintf("sudo ip route del %s/32", proxyIP)); err != nil {
		logging.Info.Printf("failed to remove specific route for proxyIP: %v", err)
	}

	// удалить маршрут к хардкод-серверу
	if _, err := executeCommand(fmt.Sprintf("sudo ip route del %s/32", "85.9.223.19")); err != nil {
		logging.Info.Printf("failed to remove specific route for 85.9.223.19: %v", err)
	}
}
