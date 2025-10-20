//go:build windows
// +build windows

package internal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"go_client/routing"

	"github.com/jackpal/gateway"
	log "github.com/sirupsen/logrus"
)

func add_route(proxyIp string) {
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		panic(err)
	}
	interfaceName, err := routing.FindInterfaceByGateway(gatewayIP.String())
	if err != nil {
		panic(err)
	}
	netInterface, err := routing.GetNetworkInterfaceByIP(interfaceName)
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
	routing.AddOrUpdateProxyRoute(proxyIp, gatewayIP.String(), netInterface.Name)
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
	interfaceName, err := routing.FindInterfaceByGateway(gatewayIP.String())
	if err != nil {
		panic(err)
	}

	netInterface, err := routing.GetNetworkInterfaceByIP(interfaceName)
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

    log.Infof("[Outline] Refreshing Shadowsocks session...")
    if err := ss.Refresh(); err != nil {
    	log.Errorf("Failed to refresh OutlineDevice: %v", err)
    	return fmt.Errorf("failed to refresh OutlineDevice: %w", err)
    }
    log.Infof("[Outline] Session refreshed successfully")

    log.Infof("[Routing] Looking up TUN interface by IP: %s", TunDeviceIP)
    tunInterface, err := routing.GetNetworkInterfaceByIP(TunDeviceIP)
    if err != nil {
    	log.Errorf("Could not find TUN interface: %v", err)
    	os.Exit(1)
    }
    log.Infof("[Routing] Found TUN interface: %s (HWAddr=%s)", tunInterface.Name, tunInterface.HardwareAddr)

    dst := tunInterface.HardwareAddr
    src := make([]byte, len(dst))
    copy(src, dst)
    src[2] += 2
    log.Infof("[Routing] Generated spoofed MAC: original=%s new=%v", tunInterface.HardwareAddr, src)

    log.Infof("[Routing] Starting routing configuration:")
    log.Infof("  Server IP:     %s", ss.GetServerIP().String())
    log.Infof("  Gateway IP:    %s", gatewayIP.String())
    log.Infof("  TUN Interface: %s", tunInterface.Name)
    log.Infof("  TUN MAC:       %s", tunInterface.HardwareAddr.String())
    log.Infof("  Net Interface: %s", netInterface.Name)
    log.Infof("  Tun Gateway:   %s", TunGateway)
    log.Infof("  Tun Device IP: %s", TunDeviceIP)

    if err := routing.StartRouting(
    	ss.GetServerIP().String(),
    	gatewayIP.String(),
    	tunInterface.Name,
    	tunInterface.HardwareAddr.String(),
    	netInterface.Name,
    	TunGateway,
    	TunDeviceIP,
    	src,
    ); err != nil {
    	log.Errorf("Failed to configure routing: %v", err)
    	return fmt.Errorf("failed to configure routing: %w", err)
    }

    log.Infof("[Routing] Routing successfully configured")

    defer func() {
    	log.Infof("[Routing] Cleaning up routes for %s...", ss.GetServerIP().String())
    	routing.StopRouting(ss.GetServerIP().String(), tunInterface.Name, gatewayIP.String(), netInterface.Name)
    	log.Infof("[Routing] Routes cleaned up")
    }()

    var closeOnce sync.Once
    closeAll := func() {
        closeOnce.Do(func() {
            log.Infof("[Outline] Closing interfaces")
            _ = tun.Close()
            _ = ss.Close()
        })
    }

    defer closeAll()

	go func() {
        <-ctx.Done()
        closeAll()
        log.Infof("[Outline] Cancel received — closing interfaces")
    }()

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
 					//log.Printf("Read %d bytes from tun\n", n)
					//log.Printf("Data from tun: % x\n", buffer[:n])
					ipPacket, err := ExtractIPPacketFromEthernet(buffer[:n])
					if err != nil {
						fmt.Println("Error:", err)
                        continue
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
					//log.Printf("Read %d bytes from OutlineDevice\n", n)
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

	log.Infof("Outline/app: Start trafficCopyWg...\n")

	trafficCopyWg.Wait()

	log.Infof("Outline/app: received interrupt signal, terminating...\n")

	tun.Close()
	ss.Close()

	return nil

}
