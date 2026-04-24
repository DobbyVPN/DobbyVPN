//go:build windows && !(android || ios)
// +build windows,!android,!ios

package internal

import (
	"context"
	"fmt"
	"go_module/tunnel/platform_engine"
	"go_module/tunnel/protected_dialer"
	"time"

	"go_module/common"
	"go_module/routing"
	"go_module/tunnel"

	"go_module/log"
	outlineCommon "go_module/outline/common"

	"github.com/jackpal/gateway"
)

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
	cfg := common.GetNetworkConfig()

	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		signalInit(initResult, err)
		return err
	}

	interfaceName, err := routing.FindInterfaceIPByGateway(gatewayIP.String())
	if err != nil {
		signalInit(initResult, err)
		return err
	}

	netInterface, err := routing.GetNetworkInterfaceByIP(interfaceName)
	if err != nil {
		err = fmt.Errorf("failed to get network interface by IP %s: %w", interfaceName, err)
		log.SimpleErrorf(Category, "[Routing] %v", err)
		signalInit(initResult, err)
		return err
	}

	serverIP, err := ResolveServerIPFromConfig(*app.TransportConfig)
	if err != nil {
		signalInit(initResult, err)
		return err
	}

	// protect route to VPN server
	if serverIP.String() != "127.0.0.1" {
		log.SimpleDebugf(Category, "[Routing] Adding early route for server %s via %s", serverIP.String(), gatewayIP.String())
		common.Client.MarkInCriticalSection(outlineCommon.Name)
		routing.AddOrUpdateProxyRoute(serverIP.String(), gatewayIP.String(), netInterface.Name)
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
		log.SimpleDebugf(Category, "[Routing] Early server route added successfully")
	} else {
		log.SimpleDebugf(Category, "[Routing] Skipping early route for localhost (Cloak mode)")
	}

	// SOCKS (Outline)
	ss, err := NewOutlineDevice(*app.TransportConfig)
	if err != nil {
		err = fmt.Errorf("failed to create OutlineDevice: %w", err)
		signalInit(initResult, err)
		return err
	}
	defer ss.Close()

	log.SimpleDebugf(Category, "[Windows] Starting tun2socks in wintun mode")
	log.SimpleDebugf(Category, "[Windows] Uplink interface: %s", netInterface.Name)
	log.SimpleDebugf(Category, "[Windows] Proxy addr: %s", ss.GetProxyAddr())

	idx, err := protected_dialer.GetDefaultInterfaceIndex()
	if err != nil {
		err = fmt.Errorf("failed to get default interface index: %w", err)
		signalInit(initResult, err)
		return err
	}
	protected_dialer.SetDefaultInterfaceIndex(idx)

	err = tunnel.StartEngine(platform_engine.EngineConfig{
		ProxyAddr:   ss.GetProxyAddr(),
		FD:          -1,
		UplinkIface: netInterface.Name,
	})
	if err != nil {
		log.SimpleErrorf(Category, "Can't start tun2socks: %v", err)
		return err
	}

	tunInterface, err := routing.WaitForInterfaceByIP(cfg.TunDevice, 5*time.Second)
	if err != nil {
		tunnel.StopEngine()
		signalInit(initResult, err)
		return err
	}

	// routing
	if err := routing.StartRouting(
		serverIP.String(),
		gatewayIP.String(),
		tunInterface.Name,
		netInterface.Name,
		cfg.TunGateway,
		cfg.TunDevice,
	); err != nil {
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
		tunnel.StopEngine()
		err = fmt.Errorf("failed to configure routing: %w", err)
		log.SimpleErrorf(Category, "[Routing] %v", err)
		signalInit(initResult, err)
		return err
	}
	common.Client.MarkOutOffCriticalSection(outlineCommon.Name)

	log.SimpleInfof(Category, "[Routing] Routing successfully configured")

	// Signal successful initialization - connection is ready
	signalInit(initResult, nil)

	defer func() {
		common.Client.MarkInCriticalSection(outlineCommon.Name)
		log.SimpleDebugf(Category, "[Routing] Cleaning up routes for %s...", serverIP.String())
		routing.StopRouting(serverIP.String(), tunInterface.Name, gatewayIP.String(), netInterface.Name, cfg.TunGateway)
		log.SimpleDebugf(Category, "[Routing] Routes cleaned up")
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)

		log.SimpleDebugf(Category, "[Tunnel] Stopping tun2socks engine")
		tunnel.StopEngine()
	}()

	<-ctx.Done()

	log.SimpleDebugf(Category, "[Tunnel] Context cancelled, shutting down...")
	log.SimpleDebugf(Category, "Outline/app: received interrupt signal, terminating...")

	return nil
}
