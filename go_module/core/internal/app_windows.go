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
	"go_module/core/pkg"
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

func (app *App) Run(ctx context.Context, initResult chan<- error) error {
	startedAt := time.Now()
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

	stepStartedAt := time.Now()
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		err = fmt.Errorf("failed to discover gateway: %w", err)
		signalInit(initResult, err)
		return err
	}
	log.Debugf(coreCommon.Category, "[Windows] DiscoverGateway gateway=%s elapsed=%s total=%s", gatewayIP.String(), time.Since(stepStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	stepStartedAt = time.Now()
	interfaceName, err := routing.FindInterfaceIPByGateway(gatewayIP.String())
	if err != nil {
		err = fmt.Errorf("failed to find interface IP by gateway %s: %w", gatewayIP.String(), err)
		signalInit(initResult, err)
		return err
	}
	log.Debugf(coreCommon.Category, "[Windows] FindInterfaceIPByGateway ip=%s elapsed=%s total=%s", interfaceName, time.Since(stepStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	stepStartedAt = time.Now()
	netInterface, err := routing.GetNetworkInterfaceByIP(interfaceName)
	if err != nil {
		err = fmt.Errorf("failed to get network interface by IP %s: %w", interfaceName, err)
		log.Debugf(coreCommon.Category, "%v", err)
		signalInit(initResult, err)
		return err
	}
	log.Debugf(coreCommon.Category, "[Windows] GetNetworkInterfaceByIP iface=%s elapsed=%s total=%s", netInterface.Name, time.Since(stepStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	stepStartedAt = time.Now()
	serverIP := app.ProtocolDevice.GetServerIP()
	if serverIP == nil {
		err = fmt.Errorf("server IP is nil")
		signalInit(initResult, err)
		return err
	}
	log.Debugf(coreCommon.Category, "Server IP resolved: %s elapsed=%s total=%s", serverIP.String(), time.Since(stepStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	// protect route to VPN server
	earlyRouteInstalled := false
	if serverIP.String() != "127.0.0.1" {
		log.Debugf(coreCommon.Category, "Adding early route for server %s via %s", serverIP.String(), gatewayIP.String())
		common.Client.MarkInCriticalSection(coreCommon.Name)
		stepStartedAt = time.Now()
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
		log.Debugf(coreCommon.Category, "Early server route added successfully changed=%v elapsed=%s total=%s", routeChanged, time.Since(stepStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))
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

	stepStartedAt = time.Now()
	protected_dialer.SetDefaultRoute(gatewayIP.String(), netInterface.Name, netInterface.Index)
	log.Debugf(coreCommon.Category, "[Windows] Default interface index=%d elapsed=%s total=%s", netInterface.Index, time.Since(stepStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	// SOCKS protocol device
	stepStartedAt = time.Now()
	err = app.ProtocolDevice.Open(app.RoutingConfig.RoutingTableID, netInterface.Name)
	if err != nil {
		cleanupEarlyRoute("ProtocolDevice error")
		err = fmt.Errorf("failed to create ProtocolDevice: %w", err)
		signalInit(initResult, err)
		return err
	}
	log.Debugf(coreCommon.Category, "[Windows] ProtocolDevice.Open OK proxy=%s elapsed=%s total=%s", app.ProtocolDevice.GetProxyAddr(), time.Since(stepStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	log.Debugf(coreCommon.Category, "[Windows] Starting tun2socks in wintun mode")
	log.Debugf(coreCommon.Category, "[Windows] Uplink interface: %s", netInterface.Name)
	log.Debugf(coreCommon.Category, "[Windows] Proxy addr: %s", app.ProtocolDevice.GetProxyAddr())

	stepStartedAt = time.Now()
	err = tunnel.StartEngine(platform_engine.EngineConfig{
		ProxyAddr:   app.ProtocolDevice.GetProxyAddr(),
		FD:          -1,
		UplinkIface: netInterface.Name,
	})
	if err != nil {
		log.Debugf(coreCommon.Category, "Can't start tun2socks: %v", err)
		if closeErr := app.ProtocolDevice.Close(); closeErr != nil {
			log.Debugf(coreCommon.Category, "[Windows] ProtocolDevice.Close after tun2socks start error failed: %v", closeErr)
		}
		cleanupEarlyRoute("tun2socks start error")
		return err
	}
	log.Debugf(coreCommon.Category, "[Windows] tunnel.StartEngine OK elapsed=%s total=%s", time.Since(stepStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	stepStartedAt = time.Now()
	tunInterface, err := routing.WaitForInterfaceByIP(cfg.TunDevice, 5*time.Second)
	if err != nil {
		tunnel.StopEngine()
		if closeErr := app.ProtocolDevice.Close(); closeErr != nil {
			log.Debugf(coreCommon.Category, "[Windows] ProtocolDevice.Close after TUN interface wait error failed: %v", closeErr)
		}
		cleanupEarlyRoute("TUN interface wait error")
		signalInit(initResult, err)
		return err
	}
	log.Debugf(coreCommon.Category, "[Windows] WaitForInterfaceByIP OK iface=%s elapsed=%s total=%s", tunInterface.Name, time.Since(stepStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	// routing
	common.Client.MarkInCriticalSection(coreCommon.Name)
	stepStartedAt = time.Now()
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
		if closeErr := app.ProtocolDevice.Close(); closeErr != nil {
			log.Debugf(coreCommon.Category, "[Windows] ProtocolDevice.Close after routing error failed: %v", closeErr)
		}
		err = fmt.Errorf("failed to configure routing: %w", err)
		log.Debugf(coreCommon.Category, "%v", err)
		signalInit(initResult, err)
		return err
	}
	common.Client.MarkOutOffCriticalSection(coreCommon.Name)

	log.Debugf(coreCommon.Category, "Routing successfully configured elapsed=%s total=%s", time.Since(stepStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	app.mu.Lock()
	app.currentDevice = app.ProtocolDevice
	app.gatewayIP = gatewayIP.String()
	app.uplinkIface = netInterface.Name
	app.tunIface = tunInterface.Name
	app.serverIP = serverIP.String()
	app.running = true
	app.mu.Unlock()

	// Signal successful initialization - connection is ready
	log.Debugf(coreCommon.Category, "[Windows] App initialization ready total=%s", time.Since(startedAt).Truncate(time.Millisecond))
	signalInit(initResult, nil)

	defer func() {
		app.mu.Lock()
		currentDevice := app.currentDevice
		currentServerIP := app.serverIP
		currentGatewayIP := app.gatewayIP
		currentUplinkIface := app.uplinkIface
		currentTunIface := app.tunIface
		app.currentDevice = nil
		app.running = false
		app.mu.Unlock()

		common.Client.MarkInCriticalSection(coreCommon.Name)
		log.Debugf(coreCommon.Category, "Cleaning up routes for %s...", currentServerIP)
		routing.StopRouting(currentServerIP, currentTunIface, currentGatewayIP, currentUplinkIface, cfg.TunGateway)
		log.Debugf(coreCommon.Category, "Routes cleaned up")
		common.Client.MarkOutOffCriticalSection(coreCommon.Name)

		log.Debugf(coreCommon.Category, "[Tunnel] Stopping tun2socks engine")
		tunnel.StopEngine()
		if currentDevice != nil {
			if closeErr := currentDevice.Close(); closeErr != nil {
				log.Debugf(coreCommon.Category, "[Windows] ProtocolDevice.Close during shutdown failed: %v", closeErr)
			}
		}
	}()

	<-ctx.Done()

	log.Debugf(coreCommon.Category, "[Tunnel] Context cancelled, shutting down...")
	log.Debugf(coreCommon.Category, "Core/app: received interrupt signal, terminating...")

	return nil
}

func (app *App) SwitchProtocolDevice(device pkg.ProtocolDevice) error {
	startedAt := time.Now()
	if app == nil {
		return fmt.Errorf("core app is not initialized")
	}
	if device == nil {
		return fmt.Errorf("protocol device is not initialized")
	}
	if app.RoutingConfig == nil {
		return fmt.Errorf("routing config is not initialized")
	}

	app.mu.Lock()
	defer app.mu.Unlock()

	if !app.running || app.currentDevice == nil {
		return fmt.Errorf("core app is not running")
	}

	newServerIP := device.GetServerIP()
	if newServerIP == nil {
		return fmt.Errorf("server IP is nil")
	}

	oldDevice := app.currentDevice
	oldServerIP := app.serverIP
	gatewayIP := app.gatewayIP
	uplinkIface := app.uplinkIface

	log.Debugf(coreCommon.Category, "[Windows] Hot-switch protocol begin oldServer=%s newServer=%s", oldServerIP, newServerIP.String())

	newRouteChanged := false
	if newServerIP.String() != "127.0.0.1" {
		common.Client.MarkInCriticalSection(coreCommon.Name)
		routeChanged, err := routing.EnsureProxyRoute(newServerIP.String(), gatewayIP, uplinkIface)
		common.Client.MarkOutOffCriticalSection(coreCommon.Name)
		if err != nil {
			return fmt.Errorf("failed to add route for new server: %w", err)
		}
		newRouteChanged = routeChanged
		log.Debugf(coreCommon.Category, "[Windows] Hot-switch route ready newServer=%s changed=%v elapsed=%s", newServerIP.String(), routeChanged, time.Since(startedAt).Truncate(time.Millisecond))
	}

	if err := device.Open(app.RoutingConfig.RoutingTableID, uplinkIface); err != nil {
		if newRouteChanged {
			common.Client.MarkInCriticalSection(coreCommon.Name)
			if cleanupErr := routing.DeleteProxyRoute(newServerIP.String(), gatewayIP, uplinkIface); cleanupErr != nil {
				log.Debugf(coreCommon.Category, "[Windows] Hot-switch cleanup new route failed after open error: %v", cleanupErr)
			}
			common.Client.MarkOutOffCriticalSection(coreCommon.Name)
		}
		return fmt.Errorf("failed to open new protocol device: %w", err)
	}
	log.Debugf(coreCommon.Category, "[Windows] Hot-switch ProtocolDevice.Open OK proxy=%s elapsed=%s", device.GetProxyAddr(), time.Since(startedAt).Truncate(time.Millisecond))

	if err := tunnel.SwitchVPNProxy(device.GetProxyAddr()); err != nil {
		_ = device.Close()
		if newRouteChanged {
			common.Client.MarkInCriticalSection(coreCommon.Name)
			if cleanupErr := routing.DeleteProxyRoute(newServerIP.String(), gatewayIP, uplinkIface); cleanupErr != nil {
				log.Debugf(coreCommon.Category, "[Windows] Hot-switch cleanup new route failed after switch error: %v", cleanupErr)
			}
			common.Client.MarkOutOffCriticalSection(coreCommon.Name)
		}
		return fmt.Errorf("failed to switch tun2socks proxy: %w", err)
	}

	app.ProtocolDevice = device
	app.currentDevice = device
	app.serverIP = newServerIP.String()

	if oldDevice != nil {
		if err := oldDevice.Close(); err != nil {
			log.Debugf(coreCommon.Category, "[Windows] Hot-switch old ProtocolDevice.Close failed: %v", err)
		}
	}
	if oldServerIP != "" && oldServerIP != newServerIP.String() {
		common.Client.MarkInCriticalSection(coreCommon.Name)
		if err := routing.DeleteProxyRoute(oldServerIP, gatewayIP, uplinkIface); err != nil {
			log.Debugf(coreCommon.Category, "[Windows] Hot-switch old server route cleanup failed oldServer=%s err=%v", oldServerIP, err)
		}
		common.Client.MarkOutOffCriticalSection(coreCommon.Name)
	}

	log.Debugf(coreCommon.Category, "[Windows] Hot-switch protocol done oldServer=%s newServer=%s proxy=%s elapsed=%s", oldServerIP, newServerIP.String(), device.GetProxyAddr(), time.Since(startedAt).Truncate(time.Millisecond))
	return nil
}
