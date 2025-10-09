//go:build windows
// +build windows

package internal

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"go_client/routing"

	"github.com/jackpal/gateway"
	log "github.com/sirupsen/logrus"
)

func add_route(proxyIp string) {
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		panic(err)
	}
	interfaceName, err := FindInterfaceByGateway(gatewayIP.String())
	if err != nil {
		panic(err)
	}
	netInterface, err := GetNetworkInterfaceByIP(interfaceName)
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
	routing.AddOrUpdateProxyRoute(proxyIp, gatewayIP.String(), netInterface.Name)
}

func FindInterfaceByGateway(gatewayIP string) (string, error) {
	cmd := exec.Command("route", "print")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
	}
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("fail to execute a command route print: %v", err)
	}

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	var foundGateway bool
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, gatewayIP) {
			foundGateway = true
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				interfaceName := parts[3]
				return interfaceName, nil
			}
		}
	}

	if !foundGateway {
		return "", fmt.Errorf("gateway %s is not found in the table", gatewayIP)
	}

	return "", fmt.Errorf("no interface %s", gatewayIP)
}

func GetNetworkInterfaceByIP(currentIP string) (*net.Interface, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("error getting network interfaces: %v", err)
	}

	for _, interf := range interfaces {
		addrs, err := interf.Addrs()
		if err != nil {
			return nil, fmt.Errorf("error getting addresses for interface %v: %v", interf.Name, err)
		}

		for _, addr := range addrs {
			if strings.Contains(addr.String(), currentIP) {
				return &interf, nil
			}
		}
	}

	return nil, fmt.Errorf("no interface found with IP: %v", currentIP)
}

func CreateEthernetPacket(dstMAC, srcMAC, ipPacket []byte) ([]byte, error) {
	if len(ipPacket) == 0 {
		return nil, errors.New("IP-packet is empty")
	}
	if len(dstMAC) != 6 || len(srcMAC) != 6 {
		return nil, errors.New("MAC addresses must be exactly 6 bytes long")
	}

	ethertype := []byte{0x08, 0x00} // Ethertype для IP

	ethernetPacket := append(dstMAC, srcMAC...)
	ethernetPacket = append(ethernetPacket, ethertype...)
	ethernetPacket = append(ethernetPacket, ipPacket...)

	return ethernetPacket, nil
}

func ExtractIPPacketFromEthernet(ethernetPacket []byte) ([]byte, error) {
	if len(ethernetPacket) < 14 {
		return nil, errors.New("packet is too short for Ethernet-title")
	}

	ethertype := (uint16(ethernetPacket[12]) << 8) | uint16(ethernetPacket[13])
	if ethertype != 0x0800 {
		return nil, errors.New("packet doesn't contain IP-data")
	}

	return ethernetPacket[14:], nil
}

func (app App) Run(ctx context.Context) error {
	trafficCopyWg := &sync.WaitGroup{}
	defer trafficCopyWg.Wait()

	if !checkRoot() {
		return errors.New("this operation requires superuser privileges. Please run the program with administrator")
	}

	TunGateway := "10.0.85.1"
	TunDeviceIP := "10.0.85.2"

	// 	TunDeviceIP := app.RoutingConfig.TunDeviceIP
	//     TunGatewayCIDR := app.RoutingConfig.TunGatewayCIDR
	//     TunGateway := strings.Split(TunGatewayCIDR, "/")[0]

	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		panic(err)
	}
	interfaceName, err := FindInterfaceByGateway(gatewayIP.String())
	if err != nil {
		panic(err)
	}

	netInterface, err := GetNetworkInterfaceByIP(interfaceName)
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}

	tun, err := newTunDevice(app.RoutingConfig.TunDeviceName, TunDeviceIP)
	if err != nil {
		return fmt.Errorf("failed to create tun device: %w", err)
	}
	defer tun.Close()

	ss, err := NewOutlineDevice(*app.TransportConfig)
	if err != nil {
		return fmt.Errorf("failed to create OutlineDevice: %w", err)
	}
	log.Infof("Create Device")
	defer ss.Close()

	if err := ss.Refresh(); err != nil {
		return fmt.Errorf("failed to refresh OutlineDevice: %w", err)
	}

	tunInterface, err := GetNetworkInterfaceByIP(TunDeviceIP)
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}

	dst := tunInterface.HardwareAddr
	src := make([]byte, len(dst))
	copy(src, dst)
	src[2] += 2

	if err := routing.StartRouting(ss.GetServerIP().String(), gatewayIP.String(), tunInterface.Name, tunInterface.HardwareAddr.String(), netInterface.Name, TunGateway, TunDeviceIP, src); err != nil {
		return fmt.Errorf("failed to configure routing: %w", err)
	}
	defer routing.StopRouting(ss.GetServerIP().String(), tunInterface.Name)

	/*ss1, err := NewOutlineDevice("ss://Y2hhY2hhMjAtaWV0Zi1wb2x5MTMwNTpaVWVmTzExenFzN0pQbFBBMU4xWHlh@195.201.111.36:40287/?outline=1")
	    if err != nil {
	        return fmt.Errorf("failed to create OutlineDevice: %w", err)
	    }

	    if err = startRouting(ss1.GetServerIP().String(), gatewayIP.String(), tunInterface.Name, tunInterface.HardwareAddr.String(), netInterface.Name, TunGateway, TunDeviceIP, src); err != nil {
		return fmt.Errorf("failed to configure routing: %w", err)
	    }
	    defer stopRouting(ss1.GetServerIP().String(), tunInterface.Name)*/

	trafficCopyWg.Add(2)
	go func() {
		defer trafficCopyWg.Done()
		buffer := make([]byte, 65000)

		for {
			select {
			case <-ctx.Done():
				return
			default:
				copy(buffer, make([]byte, len(buffer)))
				n, err := tun.Read(buffer)
				if err != nil {
					//fmt.Printf("Error reading from device: %x %v\n", n, err)
					break
				}
				if n > 0 {
					log.Printf("Read %d bytes from tun\n", n)
					//log.Printf("Data from tun: % x\n", buffer[:n])
					ipPacket, err := ExtractIPPacketFromEthernet(buffer[:n])
					if err != nil {
						fmt.Println("Error:", err)
					}
					_, err = ss.Write(ipPacket)
					if err != nil {
						//   log.Printf("Error writing to device: %v", err)
						break
					}
				}
			}
		}
	}()

	go func() {
		defer trafficCopyWg.Done()
		buf := make([]byte, 65000)
		for {
			select {
			case <-ctx.Done():
				return
			default:
				copy(buf, make([]byte, len(buf)))
				n, err := ss.Read(buf)
				if err != nil {
					//  fmt.Printf("Error reading from device: %v\n", err)
					break
				}
				if n > 0 {
					log.Printf("Read %d bytes from OutlineDevice\n", n)
					//log.Printf("Data from OutlineDevice: % x\n", buf[:n])

					ethernetPacket, err := CreateEthernetPacket(dst, src, buf[:n])
					if err != nil {
						log.Printf("Error creating Ethernet packet: %v", err)
						break
					}

					_, err = tun.Write(ethernetPacket)
					if err != nil {
						//    log.Printf("Error writing to tun: %v", err)
						break
					}
				}

			}
		}
		log.Printf("OutlineDevice -> tun stopped")
	}()

	trafficCopyWg.Wait()

	trafficCopyWg.Wait()

	log.Infof("Outline/app: received interrupt signal, terminating...\n")

	tun.Close()
	ss.Close()
	routing.StopRouting(ss.GetServerIP().String(), tunInterface.Name)

	return nil

}
