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

	coreCommon "go_module/core/common"
	"go_module/log"

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
	if app.ProtocolDevice == nil {
		err := fmt.Errorf("protocol device is not initialized")
		signalInit(initResult, err)
		return err
	}
	if app.RoutingConfig == nil {
		err := fmt.Errorf("routing config is not initialized")
		signalInit(initResult, err)
		return err
	}

	cfg := common.GetNetworkConfig()

	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		err = fmt.Errorf("failed to discover gateway: %w", err)
		signalInit(initResult, err)
		return err
	}

	interfaceName, err := routing.FindInterfaceIPByGateway(gatewayIP.String())
	if err != nil {
		err = fmt.Errorf("failed to find interface IP by gateway %s: %w", gatewayIP.String(), err)
		signalInit(initResult, err)
		return err
	}

	netInterface, err := routing.GetNetworkInterfaceByIP(interfaceName)
	if err != nil {
		err = fmt.Errorf("failed to get network interface by IP %s: %w", interfaceName, err)
		log.Debugf(coreCommon.Category, "%v", err)
		signalInit(initResult, err)
		return err
	}

	serverIP := app.ProtocolDevice.GetServerIP()
	if serverIP == nil {
		err = fmt.Errorf("server IP is nil")
		signalInit(initResult, err)
		return err
	}
	log.Debugf(coreCommon.Category, "Server IP resolved: %s", serverIP.String())

	// protect route to VPN server
	earlyRouteInstalled := false
	if serverIP.String() != "127.0.0.1" {
		log.Debugf(coreCommon.Category, "Adding early route for server %s via %s", serverIP.String(), gatewayIP.String())
		common.Client.MarkInCriticalSection(coreCommon.Name)
		var routeChanged bool
		routeChanged, err = routing.EnsureProxyRoute(serverIP.String(), gatewayIP.String(), netInterface.Name)
		if err != nil {
			common.Client.MarkOutOffCriticalSection(coreCommon.Name)
			err = fmt.Errorf("failed to add early route for server: %w", err)
			signalInit(initResult, err)
			return err
		}
		common.Client.MarkOutOffCriticalSection(coreCommon.Name)
		earlyRouteInstalled = routeChanged
		log.Debugf(coreCommon.Category, "Early server route added successfully")
	} else {
		log.Debugf(coreCommon.Category, "Skipping early route for localhost (Cloak mode)")
	}
	cleanupEarlyRoute := func(reason string) {
		if !earlyRouteInstalled {
			return
		}
		common.Client.MarkInCriticalSection(coreCommon.Name)
		log.Debugf(coreCommon.Category, "Removing early server route after %s", reason)
		if cleanupErr := routing.DeleteProxyRoute(serverIP.String(), gatewayIP.String(), netInterface.Name); cleanupErr != nil {
			log.Debugf(coreCommon.Category, "Failed to remove early server route after %s: %v", reason, cleanupErr)
		}
		common.Client.MarkOutOffCriticalSection(coreCommon.Name)
	}

	// SOCKS protocol device
	err = app.ProtocolDevice.Open(app.RoutingConfig.RoutingTableID, netInterface.Name)
	if err != nil {
		cleanupEarlyRoute("ProtocolDevice error")
		err = fmt.Errorf("failed to create ProtocolDevice: %w", err)
		signalInit(initResult, err)
		return err
	}
	defer app.ProtocolDevice.Close()

	log.Debugf(coreCommon.Category, "[Windows] Starting tun2socks in wintun mode")
	log.Debugf(coreCommon.Category, "[Windows] Uplink interface: %s", netInterface.Name)
	log.Debugf(coreCommon.Category, "[Windows] Proxy addr: %s", app.ProtocolDevice.GetProxyAddr())

	idx, err := protected_dialer.GetDefaultInterfaceIndex()
	if err != nil {
		cleanupEarlyRoute("default interface index error")
		err = fmt.Errorf("failed to get default interface index: %w", err)
		signalInit(initResult, err)
		return err
	}
	protected_dialer.SetDefaultInterfaceIndex(idx)

	err = tunnel.StartEngine(platform_engine.EngineConfig{
		ProxyAddr:   app.ProtocolDevice.GetProxyAddr(),
		FD:          -1,
		UplinkIface: netInterface.Name,
	})
	if err != nil {
		log.Debugf(coreCommon.Category, "Can't start tun2socks: %v", err)
		cleanupEarlyRoute("tun2socks start error")
		return err
	}

	tunInterface, err := routing.WaitForInterfaceByIP(cfg.TunDevice, 5*time.Second)
	if err != nil {
		tunnel.StopEngine()
		cleanupEarlyRoute("TUN interface wait error")
		signalInit(initResult, err)
		return err
	}

	// routing
	common.Client.MarkInCriticalSection(coreCommon.Name)
	if err := routing.StartRouting(
		serverIP.String(),
		gatewayIP.String(),
		tunInterface.Name,
		netInterface.Name,
		cfg.TunGateway,
		cfg.TunDevice,
	); err != nil {
		routing.StopRouting(serverIP.String(), tunInterface.Name, gatewayIP.String(), netInterface.Name, cfg.TunGateway)
		common.Client.MarkOutOffCriticalSection(coreCommon.Name)
		tunnel.StopEngine()
		err = fmt.Errorf("failed to configure routing: %w", err)
		log.Debugf(coreCommon.Category, "%v", err)
		signalInit(initResult, err)
		return err
	}
	common.Client.MarkOutOffCriticalSection(coreCommon.Name)

	log.Debugf(coreCommon.Category, "Routing successfully configured")

	// Signal successful initialization - connection is ready
	signalInit(initResult, nil)

	defer func() {
		common.Client.MarkInCriticalSection(coreCommon.Name)
		log.Debugf(coreCommon.Category, "Cleaning up routes for %s...", serverIP.String())
		routing.StopRouting(serverIP.String(), tunInterface.Name, gatewayIP.String(), netInterface.Name, cfg.TunGateway)
		log.Debugf(coreCommon.Category, "Routes cleaned up")
		common.Client.MarkOutOffCriticalSection(coreCommon.Name)

		log.Debugf(coreCommon.Category, "[Tunnel] Stopping tun2socks engine")
		tunnel.StopEngine()
	}()

	<-ctx.Done()

	log.Debugf(coreCommon.Category, "[Tunnel] Context cancelled, shutting down...")
	log.Debugf(coreCommon.Category, "Core/app: received interrupt signal, terminating...")

	return nil
}
