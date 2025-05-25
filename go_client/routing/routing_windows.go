//go:build windows

package routing

import (
	"fmt"
	log "github.com/sirupsen/logrus"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

var ipv4Subnets = []string{
	"0.0.0.0/1",
	"128.0.0.0/1",
}

var ipv4ReservedSubnets = []string{
	"0.0.0.0/8",
	"10.0.0.0/8",
	"100.64.0.0/10",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.0.0.0/24",
	"192.0.2.0/24",
	"192.31.196.0/24",
	"192.52.193.0/24",
	"192.88.99.0/24",
	"192.168.0.0/16",
	"192.175.48.0/24",
	"198.18.0.0/15",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"240.0.0.0/4",
}

const wireguardSystemConfigPath = "C:\\ProgramData\\WireGuard"

func ExecuteCommand(command string) (string, error) {
	cmd := exec.Command("cmd", "/C", command)

	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("command execution failed: %w, output: %s", err, output)
	}
	log.Infof("GoClient/routing: Command executed: %s, output: %s", command, output)
	return string(output), nil
}

func saveWireguardConf(config string, fileName string) error {
	systemConfigPath := filepath.Join(wireguardSystemConfigPath, fileName+".conf")

	err := os.MkdirAll(wireguardSystemConfigPath, os.ModePerm)
	if err != nil {
		log.Errorf("failed to create directory %s: %w", wireguardSystemConfigPath, err)
	}

	err = os.WriteFile(systemConfigPath, []byte(config), 0644)
	if err != nil {
		log.Errorf("failed to save WireGuard configuration to %s: %w", systemConfigPath, err)
	}

	log.Infof("Configuration saved successfully to %s\n", systemConfigPath)
	return nil
}

func StartTunnel(name string) {
	systemConfigPath := filepath.Join(wireguardSystemConfigPath, name+".conf")
	command := fmt.Sprintf("wireguard.exe /installtunnelservice %s", systemConfigPath)
	output, err := ExecuteCommand(command)
	if err != nil {
		log.Errorf("Failed to start tunnel: %v, output: %s", err, output)
	} else {
		log.Infof("Tunnel started successfully: %s", name)
	}
}

func StopTunnel(name string) {
	command := fmt.Sprintf("wireguard.exe /uninstalltunnelservice %s", name)
	output, err := ExecuteCommand(command)
	if err != nil {
		log.Errorf("Failed to stop tunnel: %v, output: %s", err, output)
	} else {
		log.Infof("Tunnel stopped successfully: %s", name)
	}
}

func CheckAndInstallWireGuard() error {
	_, err := exec.LookPath("wireguard.exe")
	if err != nil {
		log.Infof("WireGuard not found, installing...")

		return fmt.Errorf("WireGuard is not installed")
	}
	log.Infof("WireGuard is already installed")
	return nil
}

func StartRouting(proxyIP string, GatewayIP string, TunDeviceName string, MacAddress string, InterfaceName string, TunGateway string, TunDeviceIP string, addr []byte) error {
	log.Infof("GoClient/routing: Starting routing configuration for Windows...")
	log.Infof("GoClient/routing: Proxy IP: %s, Tun Device Name: %s, Tun Gateway: %s, Tun Device IP: %s, Gateway IP: %s, Mac Address: %s, Interface Name: %s",
		proxyIP, TunDeviceName, TunGateway, TunDeviceIP, GatewayIP, MacAddress, InterfaceName)
	log.Infof("GoClient/routing: Setting up IP rule...")
	AddOrUpdateProxyRoute(proxyIP, GatewayIP, InterfaceName)
	log.Infof("GoClient/routing: Added IP proxy rules via table\n")
	addOrUpdateReservedSubnetBypass(GatewayIP, InterfaceName)
	log.Infof("GoClient/routing: Added IP reserved rules via table\n")
	addIpv4TapRedirect(TunGateway, TunDeviceName)
	log.Infof("GoClient/routing: Added IP rules via table\n")

	log.Infof("GoClient/routing: Routing configuration completed successfully.")

	err := AddNeighbor(TunDeviceName, TunGateway, formatMACAddress(addr))
	if err != nil {
		fmt.Println("Error:", err)
	}
	return nil
}

func StopRouting(proxyIp string, TunDeviceName string) {
	log.Infof("GoClient/routing: Cleaning up routing table and rules...")
	deleteProxyRoute(proxyIp)
	removeReservedSubnetBypass()
	stopRoutingIpv4(TunDeviceName)
	log.Infof("GoClient/routing: Cleaned up routing table and rules.")
}

func AddOrUpdateProxyRoute(proxyIp string, gatewayIp string, gatewayInterfaceIndex string) {
	command := fmt.Sprintf("route change %s %s if \"%s\"", proxyIp, gatewayIp, gatewayInterfaceIndex)
	_, err := ExecuteCommand(command)
	if err != nil {
		netshCommand := fmt.Sprintf("netsh interface ipv4 add route %s/32 nexthop=%s interface=\"%s\" metric=0 store=active",
			proxyIp, gatewayIp, gatewayInterfaceIndex)
		_, err = ExecuteCommand(netshCommand)
		if err != nil {
			log.Errorf("GoClient/routing: Failed to add or update proxy route for IP %s: %v\n", proxyIp, err)
		}
	}
}

func deleteProxyRoute(proxyIp string) {
	command := fmt.Sprintf("route delete %s", proxyIp)
	_, err := ExecuteCommand(command)
	if err != nil {
		log.Errorf("GoClient/routing: Failed to delete proxy route for IP %s: %v\n", proxyIp, err)
	}
}

func addOrUpdateReservedSubnetBypass(gatewayIp string, gatewayInterfaceIndex string) {
	for _, subnet := range ipv4ReservedSubnets {
		command := fmt.Sprintf("route change %s %s if \"%s\"", subnet, gatewayIp, gatewayInterfaceIndex)
		_, err := ExecuteCommand(command)
		if err != nil {
			netshCommand := fmt.Sprintf("netsh interface ipv4 add route %s nexthop=%s interface=\"%s\" metric=0 store=active",
				subnet, gatewayIp, gatewayInterfaceIndex)
			_, err = ExecuteCommand(netshCommand)
			if err != nil {
				log.Errorf("GoClient/routing: Failed to add or update route for subnet %s: %v\n", subnet, err)
			}
		}
	}
}

func removeReservedSubnetBypass() {
	for _, subnet := range ipv4ReservedSubnets {
		command := fmt.Sprintf("route delete %s", subnet)
		_, err := ExecuteCommand(command)
		if err != nil {
			log.Errorf("GoClient/routing: Failed to delete route for subnet %s: %v\n", subnet, err)
		}
	}
}

func addIpv4TapRedirect(tapGatewayIP string, tapDeviceName string) {
	for _, subnet := range ipv4Subnets {
		command := fmt.Sprintf("netsh interface ipv4 add route %s nexthop=%s interface=\"%s\" metric=0 store=active",
			subnet, tapGatewayIP, tapDeviceName)
		_, err := ExecuteCommand(command)
		if err != nil {
			setCommand := fmt.Sprintf("netsh interface ipv4 set route %s nexthop=%s interface=\"%s\" metric=0 store=active",
				subnet, tapGatewayIP, tapDeviceName)
			_, err = ExecuteCommand(setCommand)
			if err != nil {
				log.Errorf("GoClient/routing: Failed to add or set route for subnet %s: %v\n", subnet, err)
			}
		}
	}
}

func stopRoutingIpv4(loopbackInterfaceIndex string) {
	for _, subnet := range ipv4Subnets {
		command := fmt.Sprintf("netsh interface ipv4 add route %s interface=\"%s\" metric=0 store=active", subnet, loopbackInterfaceIndex)
		_, err := ExecuteCommand(command)
		if err != nil {
			setCommand := fmt.Sprintf("netsh interface ipv4 set route %s interface=\"%s\" metric=0 store=active", subnet, loopbackInterfaceIndex)
			_, err = ExecuteCommand(setCommand)
			if err != nil {
				log.Errorf("GoClient/routing: Failed to add or set route for subnet %s: %v\n", subnet, err)
			}
		}
	}
}

func formatMACAddress(mac []byte) string {
	return strings.ToUpper(fmt.Sprintf("%02X-%02X-%02X-%02X-%02X-%02X", mac[0], mac[1], mac[2], mac[3], mac[4], mac[5]))
}

func AddNeighbor(interfaceName, gatewayIP, macAddress string) error {
	netshCommand := fmt.Sprintf(
		`netsh interface ip add neighbors "%s" "%s" "%s"`,
		interfaceName, gatewayIP, macAddress,
	)

	cmd := exec.Command("cmd", "/C", netshCommand)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
	}
	output, err := cmd.CombinedOutput()
	if err == nil {
		fmt.Printf("Command arp executed successfully: %s\n", string(output))
	}
	return nil
}
