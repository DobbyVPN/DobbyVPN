//go:build darwin && !(android || ios)
// +build darwin,!android,!ios

package internal

import (
	"context"
	"fmt"
	"go_module/log"
	"go_module/tunnel/platform_engine"
	"go_module/tunnel/protected_dialer"
	"sync"

	"go_module/common"
	coreCommon "go_module/core/common"
	"go_module/routing"
	"go_module/tunnel"

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

	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		err = fmt.Errorf("failed to discover gateway: %w", err)
		signalInit(initResult, err)
		return err
	}

	log.Infof("[Network] Default gateway detected: %s", gatewayIP.String())

	serverIP := app.ProtocolDevice.GetServerIP()
	if serverIP == nil {
		err = fmt.Errorf("server IP is nil")
		signalInit(initResult, err)
		return err
	}
	log.Infof("[Routing] Server IP resolved: %s", serverIP.String())

	earlyRouteInstalled := false
	if serverIP.String() != "127.0.0.1" {
		log.Infof("[Routing] Adding direct route for VPN server %s via gateway %s (bypass VPN)", serverIP.String(), gatewayIP.String())
		common.Client.MarkInCriticalSection(coreCommon.Name)
		var routeChanged bool
		routeChanged, err = routing.EnsureProxyRoute(serverIP.String(), gatewayIP.String())
		if err != nil {
			common.Client.MarkOutOffCriticalSection(coreCommon.Name)
			err = fmt.Errorf("failed to add early route for server: %w", err)
			signalInit(initResult, err)
			return err
		}
		common.Client.MarkOutOffCriticalSection(coreCommon.Name)
		earlyRouteInstalled = routeChanged
		log.Infof("[Routing] Direct route for VPN server installed")
	} else {
		log.Infof("[Routing] Skipping direct route for localhost (Cloak mode)")
	}

	log.Infof("[TUN] Initializing virtual interface (utun)...")

	err = app.ProtocolDevice.Open(app.RoutingConfig.RoutingTableID, "")
	if err != nil {
		if earlyRouteInstalled {
			common.Client.MarkInCriticalSection(coreCommon.Name)
			if cleanupErr := routing.DeleteProxyRoute(serverIP.String(), gatewayIP.String()); cleanupErr != nil {
				log.Infof("[Routing] Failed to remove early server route after ProtocolDevice error: %v", cleanupErr)
			}
			common.Client.MarkOutOffCriticalSection(coreCommon.Name)
		}
		err = fmt.Errorf("failed to create ProtocolDevice: %w", err)
		signalInit(initResult, err)
		return err
	}

	log.Infof("[Protocol] ProtocolDevice successfully created")

	var closeOnce sync.Once
	closeAll := func() {
		closeOnce.Do(func() {
			log.Infof("[Lifecycle] Shutting down VPN components (tun2socks + device)")
			tunnel.StopEngine()
			_ = app.ProtocolDevice.Close()
		})
	}

	defer closeAll()

	go func() {
		<-ctx.Done()
		closeAll()
		log.Infof("[Lifecycle] Context cancelled — closing VPN interfaces")
	}()

	defer func() {
		common.Client.MarkInCriticalSection(coreCommon.Name)
		log.Infof("[Routing] Restoring system routing (removing VPN routes)...")
		routing.StopRouting(serverIP.String(), gatewayIP.String())
		log.Infof("[Routing] System default route restored via %s", gatewayIP.String())
		common.Client.MarkOutOffCriticalSection(coreCommon.Name)
	}()

	log.Infof("[Tunnel] Starting tun2socks engine (darwin/utun mode)...")

	err = tunnel.StartEngine(platform_engine.EngineConfig{
		ProxyAddr:   app.ProtocolDevice.GetProxyAddr(),
		FD:          -1,
		UplinkIface: "",
	})
	if err != nil {
		signalInit(initResult, err)
		return err
	}

	tunName := platform_engine.LastIface

	log.Infof("[Tunnel] tun2socks started, interface: %s", tunName)

	common.Client.MarkInCriticalSection(coreCommon.Name)

	log.Infof("[Routing] Switching default route to TUN interface (%s)", tunName)
	if err := routing.StartRouting(serverIP.String(), gatewayIP.String(), tunName); err != nil {
		common.Client.MarkOutOffCriticalSection(coreCommon.Name)
		err = fmt.Errorf("failed to configure routing: %w", err)
		signalInit(initResult, err)
		return err
	}
	log.Infof("[Routing] Default route is now redirected to VPN (TUN)")

	log.Infof("[Tunnel] VPN dataplane is up, starting traffic handling...")

	ifaceName, idx, err := protected_dialer.GetDefaultInterfaceNameDarwin(gatewayIP)
	if err != nil {
		log.Infof("[Darwin-Protect] ERROR: failed to detect default interface for protected sockets: %v", err)
	} else {
		log.Infof("[Darwin-Protect] Selected interface for direct traffic: %s (index=%d)", ifaceName, idx)

		protected_dialer.SetDefaultInterface(idx)

		log.Infof("[Routing] Adding scoped default route via %s -> %s (for protected traffic bypass)", ifaceName, gatewayIP.String())
		if err := routing.AddScopedDefaultRoute(ifaceName, gatewayIP.String()); err != nil {
			log.Infof("[Routing] ERROR: failed to add scoped default route via %s: %v", ifaceName, err)
		} else {
			log.Infof("[Routing] Scoped default route installed: interface=%s gateway=%s", ifaceName, gatewayIP.String())
		}
	}

	defer func() {
		if ifaceName != "" {
			log.Infof("[Routing] Removing scoped default route for interface %s", ifaceName)
			routing.DeleteScopedDefaultRoute(ifaceName)
		}
	}()

	common.Client.MarkOutOffCriticalSection(coreCommon.Name)

	log.Infof("[Lifecycle] VPN initialization completed successfully")

	signalInit(initResult, nil)

	<-ctx.Done()

	log.Infof("[Lifecycle] Context cancelled — stopping VPN engine")

	return nil
}
