//go:build linux
// +build linux

package internal

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/jackpal/gateway"
)

func (app App) Run(ctx context.Context) error {
	// Определяем gateway
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		return fmt.Errorf("failed to discover gateway: %w", err)
	}
	logging.Info.Printf("gatewayIP: %s", gatewayIP.String())

	trafficCopyWg := &sync.WaitGroup{}
	defer trafficCopyWg.Wait()

	// Создаём TUN
	logging.Info.Printf("Outline/Run: Start creating tun")
	tun, err := newTunDevice(app.RoutingConfig.TunDeviceName, app.RoutingConfig.TunDeviceIP)
	if err != nil {
		return fmt.Errorf("failed to create tun device: %w", err)
	}
	defer tun.Close()
	log.Printf("Tun created")

	// Создаём OutlineDevice
	logging.Info.Printf("Outline/Run: Start device")
	ss, err := NewOutlineDevice(*app.TransportConfig)
	if err != nil {
		return fmt.Errorf("failed to create OutlineDevice: %w", err)
	}
	defer ss.Close()
	ss.Refresh()
	log.Printf("Device created")

	// Поднимаем роутинг
	if err := startRouting(ss.GetServerIP().String(), gatewayIP.String(), app.RoutingConfig.TunDeviceName); err != nil {
		return fmt.Errorf("failed to configure routing: %w", err)
	}
	defer stopRouting(ss.GetServerIP().String(), gatewayIP.String())

	// Запускаем копирование трафика TUN ↔ Outline
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
					break
				}
				if n > 0 {
					if _, err = ss.Write(buffer[:n]); err != nil {
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
					break
				}
				if n > 0 {
					if _, err = tun.Write(buf[:n]); err != nil {
						break
					}
				}
			}
		}
		log.Printf("OutlineDevice -> tun stopped")
	}()

	trafficCopyWg.Wait()

	tun.Close()
	ss.Close()
	log.Printf("Tun and device closed")
	return nil
}
