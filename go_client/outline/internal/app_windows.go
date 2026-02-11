//go:build windows
// +build windows

package internal

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"go_client/common"
	"go_client/routing"

	"github.com/jackpal/gateway"
	log "go_client/logger"
	outlineCommon "go_client/outline/common"
)

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

// signalInit sends the initialization result to the channel (if provided) exactly once.
// After signaling, further calls are no-ops.
func signalInit(initResult chan<- error, err error) {
	if initResult != nil {
		select {
		case initResult <- err:
		default:
			// Already signaled
		}
	}
}

func (app App) Run(ctx context.Context, initResult chan<- error) error {
	trafficCopyWg := &sync.WaitGroup{}
	defer trafficCopyWg.Wait()

	if !checkRoot() {
		err := errors.New("this operation requires superuser privileges. Please run the program with administrator")
		signalInit(initResult, err)
		return err
	}

	TunGateway := "10.0.85.1"
	TunDeviceIP := "10.0.85.2"

	// 	TunDeviceIP := app.RoutingConfig.TunDeviceIP
	//     TunGatewayCIDR := app.RoutingConfig.TunGatewayCIDR
	//     TunGateway := strings.Split(TunGatewayCIDR, "/")[0]

	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		signalInit(initResult, err)
		return err
	}
	interfaceName, err := routing.FindInterfaceByGateway(gatewayIP.String())
	if err != nil {
		signalInit(initResult, err)
		return err
	}

	netInterface, err := routing.GetNetworkInterfaceByIP(interfaceName)
	if err != nil {
		log.Infof("Error:", err)
		signalInit(initResult, err)
		return err
	}

	log.Infof("[Routing] Pre-resolving server IP from config...")
	serverIP, err := ResolveServerIPFromConfig(*app.TransportConfig)
	if err != nil {
		err = fmt.Errorf("failed to resolve server IP from config: %w", err)
		signalInit(initResult, err)
		return err
	}
	log.Infof("[Routing] Server IP resolved: %s", serverIP.String())

	if serverIP.String() != "127.0.0.1" {
		log.Infof("[Routing] Adding early route for server %s via %s", serverIP.String(), gatewayIP.String())
		common.Client.MarkInCriticalSection(outlineCommon.Name)
		routing.AddOrUpdateProxyRoute(serverIP.String(), gatewayIP.String(), netInterface.Name)
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
		log.Infof("[Routing] Early server route added successfully")
	} else {
		log.Infof("[Routing] Skipping early route for localhost (Cloak mode)")
	}

	tun, err := newTunDevice(app.RoutingConfig.TunDeviceName, TunDeviceIP)
	if err != nil {
		err = fmt.Errorf("failed to create tun device: %w", err)
		signalInit(initResult, err)
		return err
	}
	defer tun.Close()

	ss, err := NewOutlineDevice(*app.TransportConfig)
	if err != nil {
		err = fmt.Errorf("failed to create OutlineDevice: %w", err)
		signalInit(initResult, err)
		return err
	}
	log.Infof("Create Device")
	defer ss.Close()

	log.Infof("[Outline] Refreshing Shadowsocks session...")
	if err := ss.Refresh(); err != nil {
		log.Infof("Failed to refresh OutlineDevice: %v", err)
		err = fmt.Errorf("failed to refresh OutlineDevice: %w", err)
		signalInit(initResult, err)
		return err
	}
	log.Infof("[Outline] Session refreshed successfully")

	log.Infof("[Routing] Looking up TUN interface by IP: %s", TunDeviceIP)
	tunInterface, err := routing.GetNetworkInterfaceByIP(TunDeviceIP)
	if err != nil {
		log.Infof("Could not find TUN interface: %v", err)
		err = fmt.Errorf("failed to find TUN interface: %w", err)
		signalInit(initResult, err)
		return err
	}
	log.Infof("[Routing] Found TUN interface: %s (HWAddr=%s)", tunInterface.Name, tunInterface.HardwareAddr)

	dst := tunInterface.HardwareAddr
	src := make([]byte, len(dst))
	copy(src, dst)
	src[2] += 2
	log.Infof("[Routing] Generated spoofed MAC: original=%s new=%v", tunInterface.HardwareAddr, src)

	log.Infof("[Routing] Starting routing configuration:")
	log.Infof("  Server IP:     %s", serverIP.String())
	log.Infof("  Gateway IP:    %s", gatewayIP.String())
	log.Infof("  TUN Interface: %s", tunInterface.Name)
	log.Infof("  TUN MAC:       %s", tunInterface.HardwareAddr.String())
	log.Infof("  Net Interface: %s", netInterface.Name)
	log.Infof("  Tun Gateway:   %s", TunGateway)
	log.Infof("  Tun Device IP: %s", TunDeviceIP)

	common.Client.MarkInCriticalSection(outlineCommon.Name)
	if err := routing.StartRouting(
		serverIP.String(),
		gatewayIP.String(),
		tunInterface.Name,
		tunInterface.HardwareAddr.String(),
		netInterface.Name,
		TunGateway,
		TunDeviceIP,
		src,
	); err != nil {
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
		log.Infof("Failed to configure routing: %v", err)
		err = fmt.Errorf("failed to configure routing: %w", err)
		signalInit(initResult, err)
		return err
	}
	common.Client.MarkOutOffCriticalSection(outlineCommon.Name)

	log.Infof("[Routing] Routing successfully configured")

	// Signal successful initialization - connection is ready
	signalInit(initResult, nil)

	defer func() {
		common.Client.MarkInCriticalSection(outlineCommon.Name)
		log.Infof("[Routing] Cleaning up routes for %s...", serverIP.String())
		routing.StopRouting(serverIP.String(), tunInterface.Name, gatewayIP.String(), netInterface.Name)
		log.Infof("[Routing] Routes cleaned up")
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
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
						log.Infof("Error creating Ethernet packet: %v", err)
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
		log.Infof("OutlineDevice -> tun stopped")
	}()

	log.Infof("Outline/app: Start trafficCopyWg...\n")

	trafficCopyWg.Wait()

	log.Infof("Outline/app: received interrupt signal, terminating...\n")

	tun.Close()
	ss.Close()

	return nil

}
