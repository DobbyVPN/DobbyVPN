//go:build darwin
// +build darwin

package internal

import (
	"errors"
	"fmt"
	"log"
	//"os/exec"
	"context"
	"sync"
	//"time"
	"github.com/jackpal/gateway"
)

//var Logging = out.Logging

func add_route(proxyIp string) {
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		panic(err)
	}

	addSpecificRoute := fmt.Sprintf("sudo route add -net %s/32 %s", proxyIp, gatewayIP.String())
	if _, err := executeCommand(addSpecificRoute); err != nil {
		logging.Info.Printf("failed to add specific route: %w", err)
	}
}

func (app App) Run(ctx context.Context) error {
	// this WaitGroup must Wait() after tun is closed

	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		panic(err)
	}

	logging.Info.Printf("gatewayIP: %s", gatewayIP.String())

	trafficCopyWg := &sync.WaitGroup{}
	defer trafficCopyWg.Wait()

	if !checkRoot() {
		return errors.New("this operation requires superuser privileges. Please run the program with sudo or as root")
	}

	tun, err := newTunDevice(app.RoutingConfig.TunDeviceName, app.RoutingConfig.TunDeviceIP)
	if err != nil {
		return fmt.Errorf("failed to create tun device: %w, open app with sudo", err)
	}
	defer tun.Close()

	log.Printf("Tun created")

	ss, err := NewOutlineDevice(*app.TransportConfig)
	if err != nil {
		return fmt.Errorf("failed to create OutlineDevice: %w", err)
	}
	defer ss.Close()

	ss.Refresh()

	log.Printf("Device created")

	// Copy the traffic from tun device to OutlineDevice bidirectionally
	trafficCopyWg.Add(2)

	go func() {
		defer trafficCopyWg.Done()
		buffer := make([]byte, 65536)

		for {
			select {
			case <-ctx.Done():
				return
			default:
				n, err := tun.Read(buffer)
				if err != nil {
					//fmt.Printf("Error reading from device: %x %v\n", n, err)
					break
				}
				if n > 0 {
					//log.Printf("Read %d bytes from tun\n", n)
					//log.Printf("Data from tun: % x\n", buffer[:n])

					_, err = ss.Write(buffer[:n])
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
		buf := make([]byte, 65536)
		for {
			select {
			case <-ctx.Done():
				return
			default:
				n, err := ss.Read(buf)
				if err != nil {
					//  fmt.Printf("Error reading from device: %v\n", err)
					break
				}
				if n > 0 {
					//log.Printf("Read %d bytes from OutlineDevice\n", n)
					//log.Printf("Data from OutlineDevice: % x\n", buf[:n])

					_, err = tun.Write(buf[:n])
					if err != nil {
						//    log.Printf("Error writing to tun: %v", err)
						break
					}
				}

			}
		}
		log.Printf("OutlineDevice -> tun stopped")
	}()

	if err := startRouting(ss.GetServerIP().String(), gatewayIP.String(), tun.(*tunDevice).name); err != nil {
		return fmt.Errorf("failed to configure routing: %w", err)
	}
	defer stopRouting(ss.GetServerIP().String(), gatewayIP.String())

	trafficCopyWg.Wait()

	trafficCopyWg.Wait()

	tun.Close()
	log.Printf("Tun closed")
	ss.Close()
	log.Printf("Device closed")
	stopRouting(ss.GetServerIP().String(), gatewayIP.String())
	log.Printf("Stopped")
	return nil
}
