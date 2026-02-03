//go:build linux
// +build linux

package internal

import (
	"context"
	"fmt"
	log "go_client/logger"
	"sync"

	"go_client/common"
	outlineCommon "go_client/outline/common"
	"go_client/routing"

	"github.com/jackpal/gateway"
)

func (app App) Run(ctx context.Context) error {
	// Define gateway
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		return fmt.Errorf("failed to discover gateway: %w", err)
	}
	log.Infof("gatewayIP: %s", gatewayIP.String())

	trafficCopyWg := &sync.WaitGroup{}
	defer trafficCopyWg.Wait()

	log.Infof("[Routing] Pre-resolving server IP from config...")
	serverIP, err := ResolveServerIPFromConfig(*app.TransportConfig)
	if err != nil {
		return fmt.Errorf("failed to resolve server IP from config: %w", err)
	}
	log.Infof("[Routing] Server IP resolved: %s", serverIP.String())

	if serverIP.String() != "127.0.0.1" {
		log.Infof("[Routing] Adding early route for server %s via %s", serverIP.String(), gatewayIP.String())
		common.Client.MarkInCriticalSection(outlineCommon.Name)
		routing.AddProxyRoute(serverIP.String(), gatewayIP.String())
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
		log.Infof("[Routing] Early server route added successfully")
	} else {
		log.Infof("[Routing] Skipping early route for localhost (Cloak mode)")
	}

	// Create TUN
	log.Infof("Outline/Run: Start creating tun")
	tun, err := newTunDevice(app.RoutingConfig.TunDeviceName, app.RoutingConfig.TunDeviceIP)
	if err != nil {
		return fmt.Errorf("failed to create tun device: %w", err)
	}
	defer tun.Close()
	log.Infof("Tun created")

	// Create OutlineDevice
	log.Infof("Outline/Run: Start device")
	ss, err := NewOutlineDevice(*app.TransportConfig)
	if err != nil {
		return fmt.Errorf("failed to create OutlineDevice: %w", err)
	}
	defer ss.Close()
	ss.Refresh()
	log.Infof("Device created")

	common.Client.MarkInCriticalSection(outlineCommon.Name)
	// Up routing
	if err := routing.StartRouting(serverIP.String(), gatewayIP.String(), app.RoutingConfig.TunDeviceName); err != nil {
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
		return fmt.Errorf("failed to configure routing: %w", err)
	}
	common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
	defer func() {
		common.Client.MarkInCriticalSection(outlineCommon.Name)
		log.Infof("[Routing] Cleaning up routes for %s...", serverIP.String())
		routing.StopRouting(serverIP.String(), gatewayIP.String())
		log.Infof("[Routing] Routes cleaned up")
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
	}()

	// Start traffic copy TUN ↔ Outline
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
		log.Infof("OutlineDevice -> tun stopped")
	}()

	trafficCopyWg.Wait()

	tun.Close()
	ss.Close()
	log.Infof("Tun and device closed")
	return nil
}
