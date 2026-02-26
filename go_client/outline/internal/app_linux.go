//go:build linux
// +build linux

package internal

import (
	"context"
	"fmt"
	"go_client/common"
	log "go_client/logger"
	outlineCommon "go_client/outline/common"
	"go_client/routing"
	"go_client/tunnel"

	"github.com/jackpal/gateway"
)

// signalInit sends the initialization result to the channel (if provided) exactly once.
func signalInit(initResult chan<- error, err error) {
	if initResult != nil {
		select {
		case initResult <- err:
		default:
		}
	}
}

func (app App) Run(ctx context.Context, initResult chan<- error) error {
	// Define gateway
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		err = fmt.Errorf("failed to discover gateway: %w", err)
		signalInit(initResult, err)
		return err
	}
	log.Infof("gatewayIP: %s", gatewayIP.String())

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
		err = fmt.Errorf("failed to create tun device: %w", err)
		signalInit(initResult, err)
		return err
	}
	defer func() { _ = tun.Close() }()
	log.Infof("Tun created")

	// Create OutlineDevice
	log.Infof("Outline/Run: Start device")
	ss, err := NewOutlineDevice(*app.TransportConfig)
	if err != nil {
		err = fmt.Errorf("failed to create OutlineDevice: %w", err)
		signalInit(initResult, err)
		return err
	}
	defer func() { _ = ss.Close() }()

	if err := ss.Refresh(); err != nil {
		err = fmt.Errorf("failed to refresh OutlineDevice: %w", err)
		signalInit(initResult, err)
		return err
	}
	log.Infof("Device created")

	common.Client.MarkInCriticalSection(outlineCommon.Name)
	// Up routing
	if err := routing.StartRouting(serverIP.String(), gatewayIP.String(), app.RoutingConfig.TunDeviceName); err != nil {
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
		err = fmt.Errorf("failed to configure routing: %w", err)
		signalInit(initResult, err)
		return err
	}
	common.Client.MarkOutOffCriticalSection(outlineCommon.Name)

	// Signal successful initialization
	signalInit(initResult, nil)

	defer func() {
		common.Client.MarkInCriticalSection(outlineCommon.Name)
		log.Infof("[Routing] Cleaning up routes for %s...", serverIP.String())
		routing.StopRouting(serverIP.String(), gatewayIP.String())
		log.Infof("[Routing] Routes cleaned up")
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
	}()

	tunnel.StartTransfer(
		tun,
		func(buf []byte) (int, error) {
			return ss.Read(buf)
		},
		func(buf []byte) (int, error) {
			return ss.Write(buf)
		},
	)

	<-ctx.Done()

	log.Infof("[Tunnel] Context cancelled, stopping transfer")
	tunnel.StopTransfer()
	log.Infof("Tun and device closed")
	return nil
}
