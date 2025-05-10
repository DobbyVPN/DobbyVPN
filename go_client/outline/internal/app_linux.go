//go:build linux
// +build linux

package internal

import (
	"context"
	"fmt"
	"log"
	"sync"
)

func (app App) Run(ctx context.Context) error {
	// this WaitGroup must Wait() after tun is closed
	trafficCopyWg := &sync.WaitGroup{}
	defer trafficCopyWg.Wait()

	logging.Info.Printf("Outline/Run: Start creating tun")
	tun, err := newTunDevice(app.RoutingConfig.TunDeviceName, app.RoutingConfig.TunDeviceIP)
	if err != nil {
		return fmt.Errorf("failed to create tun device: %w", err)
	}
	defer tun.Close()

	// disable IPv6 before resolving Shadowsocks server IP
	prevIPv6, err := enableIPv6(false)
	if err != nil {
		return fmt.Errorf("failed to disable IPv6: %w", err)
	}
	defer enableIPv6(prevIPv6)

	logging.Info.Printf("Outline/Run: Start device")
	ss, err := NewOutlineDevice(*app.TransportConfig)
	if err != nil {
		return fmt.Errorf("failed to create OutlineDevice: %w", err)
	}
	defer ss.Close()

	ss.Refresh()

	logging.Info.Printf("Outline/Run: Start routing")

	if err := startRouting(ss.GetServerIP().String(), app.RoutingConfig); err != nil {
		return fmt.Errorf("failed to configure routing: %w", err)
	}
	defer func() {
		logging.Info.Printf("Outline/Run: Stop routing")
		stopRouting(app.RoutingConfig.RoutingTableID)
		log.Printf("Outline/Run: Stopped")
	}()

	logging.Info.Printf("Outline/Run: Made table routing")

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

	trafficCopyWg.Wait()

	logging.Info.Printf("Outline/Run: Disconnected")

	tun.Close()
	logging.Info.Printf("Outline/Run: tun closed")
	ss.Close()
	logging.Info.Printf("Outline/Run: device closed")
	return nil
}
