//go:build darwin
// +build darwin

package internal

import (
	"errors"
	"fmt"
	log "github.com/sirupsen/logrus"
	//"os/exec"
	"context"
	"sync"
	//"time"

	"go_client/routing"

	"github.com/jackpal/gateway"
)

func add_route(proxyIp string) {
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		panic(err)
	}

	addSpecificRoute := fmt.Sprintf("sudo route add -net %s/32 %s", proxyIp, gatewayIP.String())
	if _, err := routing.ExecuteCommand(addSpecificRoute); err != nil {
		log.Infof("failed to add specific route: %w", err)
	}
}

func (app App) Run(ctx context.Context) error {
	// this WaitGroup must Wait() after tun is closed

	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		panic(err)
	}

	log.Infof("gatewayIP: %s", gatewayIP.String())

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

	if err := routing.StartRouting(ss.GetServerIP().String(), gatewayIP.String(), tun.(*tunDevice).name); err != nil {
		return fmt.Errorf("failed to configure routing: %w", err)
	}

    defer func() {
    	log.Infof("[Routing] Cleaning up routes for %s...", ss.GetServerIP().String())
    	routing.StopRouting(ss.GetServerIP().String(), gatewayIP.String())
    	log.Infof("[Routing] Routes cleaned up")
    }()

	log.Infof("Outline/app: Start trafficCopyWg...\n")

	trafficCopyWg.Wait()

	log.Infof("Outline/app: received interrupt signal, terminating...\n")

	tun.Close()
	ss.Close()

	return nil
}
