//go:build darwin && !(android || ios)
// +build darwin,!android,!ios

package internal

import (
	"context"
	"fmt"
	"go_module/core/pkg"
	"go_module/log"
	"go_module/tunnel/platform_engine"
	"go_module/tunnel/protected_dialer"
	"sync"
	"time"

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

func (app *App) Run(ctx context.Context, initResult chan<- error) error {
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

	log.Debugf(coreCommon.Category, "[Network] Default gateway detected: %s", gatewayIP.String())

	serverIP := app.ProtocolDevice.GetServerIP()
	if serverIP == nil {
		err = fmt.Errorf("server IP is nil")
		signalInit(initResult, err)
		return err
	}
	log.Debugf(coreCommon.Category, "[Routing] Server IP resolved: %s", serverIP.String())

	earlyRouteInstalled := false
	if serverIP.String() != "127.0.0.1" {
		log.Debugf(coreCommon.Category, "[Routing] Adding direct route for VPN server %s via gateway %s (bypass VPN)", serverIP.String(), gatewayIP.String())
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
		log.Debugf(coreCommon.Category, "[Routing] Direct route for VPN server installed")
	} else {
		log.Debugf(coreCommon.Category, "[Routing] Skipping direct route for localhost (Cloak mode)")
	}

	ifaceName, idx, err := protected_dialer.GetDefaultInterfaceNameDarwin(gatewayIP)
	if err != nil {
		log.Debugf(coreCommon.Category, "[Darwin-Protect] ERROR: failed to detect default interface for protected sockets: %v", err)
	} else {
		log.Debugf(coreCommon.Category, "[Darwin-Protect] Selected interface for direct traffic: %s (index=%d)", ifaceName, idx)
		protected_dialer.SetDefaultRoute(gatewayIP.String(), ifaceName, idx)
	}

	log.Debugf(coreCommon.Category, "[TUN] Initializing virtual interface (utun)...")

	err = app.ProtocolDevice.Open(app.RoutingConfig.RoutingTableID, ifaceName)
	if err != nil {
		if earlyRouteInstalled {
			common.Client.MarkInCriticalSection(coreCommon.Name)
			if cleanupErr := routing.DeleteProxyRoute(serverIP.String(), gatewayIP.String()); cleanupErr != nil {
				log.Debugf(coreCommon.Category, "[Routing] Failed to remove early server route after ProtocolDevice error: %v", cleanupErr)
			}
			common.Client.MarkOutOffCriticalSection(coreCommon.Name)
		}
		err = fmt.Errorf("failed to create ProtocolDevice: %w", err)
		signalInit(initResult, err)
		return err
	}

	log.Debugf(coreCommon.Category, "[Protocol] ProtocolDevice successfully created")

	var closeOnce sync.Once
	tunName := ""
	closeAll := func() {
		closeOnce.Do(func() {
			log.Debugf(coreCommon.Category, "[Lifecycle] Shutting down VPN components (tun2socks + device)")
			app.mu.Lock()
			currentDevice := app.currentDevice
			if currentDevice == nil {
				currentDevice = app.ProtocolDevice
			}
			app.currentDevice = nil
			app.running = false
			app.mu.Unlock()
			tunnel.StopEngine()
			if currentDevice != nil {
				_ = currentDevice.Close()
			}
		})
	}

	defer closeAll()

	go func() {
		<-ctx.Done()
		closeAll()
		log.Debugf(coreCommon.Category, "[Lifecycle] Context cancelled — closing VPN interfaces")
	}()

	defer func() {
		app.mu.Lock()
		currentServerIP := app.serverIP
		currentGatewayIP := app.gatewayIP
		currentTunIface := app.tunIface
		app.mu.Unlock()
		if currentServerIP == "" && serverIP != nil {
			currentServerIP = serverIP.String()
		}
		if currentGatewayIP == "" {
			currentGatewayIP = gatewayIP.String()
		}
		if currentTunIface == "" {
			currentTunIface = tunName
		}
		common.Client.MarkInCriticalSection(coreCommon.Name)
		log.Debugf(coreCommon.Category, "[Routing] Restoring system routing (removing VPN routes)...")
		routing.StopRouting(currentServerIP, currentGatewayIP, currentTunIface)
		log.Debugf(coreCommon.Category, "[Routing] System default route restored via %s", currentGatewayIP)
		common.Client.MarkOutOffCriticalSection(coreCommon.Name)
	}()

	log.Debugf(coreCommon.Category, "[Tunnel] Starting tun2socks engine (darwin/utun mode)...")

	err = tunnel.StartEngine(platform_engine.EngineConfig{
		ProxyAddr:   app.ProtocolDevice.GetProxyAddr(),
		FD:          -1,
		UplinkIface: "",
	})
	if err != nil {
		signalInit(initResult, err)
		return err
	}

	tunName = platform_engine.LastIface

	log.Debugf(coreCommon.Category, "[Tunnel] tun2socks started, interface: %s", tunName)

	common.Client.MarkInCriticalSection(coreCommon.Name)

	log.Debugf(coreCommon.Category, "[Routing] Switching default route to TUN interface (%s)", tunName)
	if err := routing.StartRouting(serverIP.String(), gatewayIP.String(), tunName); err != nil {
		common.Client.MarkOutOffCriticalSection(coreCommon.Name)
		err = fmt.Errorf("failed to configure routing: %w", err)
		signalInit(initResult, err)
		return err
	}
	log.Debugf(coreCommon.Category, "[Routing] Default route is now redirected to VPN (TUN)")

	log.Debugf(coreCommon.Category, "[Tunnel] VPN dataplane is up, starting traffic handling...")

	if ifaceName != "" {
		log.Debugf(coreCommon.Category, "[Routing] Adding scoped default route via %s -> %s (for protected traffic bypass)", ifaceName, gatewayIP.String())
		if err := routing.AddScopedDefaultRoute(ifaceName, gatewayIP.String()); err != nil {
			log.Debugf(coreCommon.Category, "[Routing] ERROR: failed to add scoped default route via %s: %v", ifaceName, err)
		} else {
			log.Debugf(coreCommon.Category, "[Routing] Scoped default route installed: interface=%s gateway=%s", ifaceName, gatewayIP.String())
		}
	}

	defer func() {
		if ifaceName != "" {
			log.Debugf(coreCommon.Category, "[Routing] Removing scoped default route for interface %s", ifaceName)
			routing.DeleteScopedDefaultRoute(ifaceName)
		}
	}()

	common.Client.MarkOutOffCriticalSection(coreCommon.Name)

	app.mu.Lock()
	app.currentDevice = app.ProtocolDevice
	app.gatewayIP = gatewayIP.String()
	app.uplinkIface = ifaceName
	app.tunIface = tunName
	app.serverIP = serverIP.String()
	app.running = true
	app.mu.Unlock()

	log.Debugf(coreCommon.Category, "[Lifecycle] VPN initialization completed successfully")

	signalInit(initResult, nil)

	<-ctx.Done()

	log.Debugf(coreCommon.Category, "[Lifecycle] Context cancelled — stopping VPN engine")

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

	log.Debugf(coreCommon.Category, "[Darwin] Hot-switch protocol begin oldServer=%s newServer=%s", oldServerIP, newServerIP.String())

	newRouteChanged := false
	if newServerIP.String() != "127.0.0.1" {
		common.Client.MarkInCriticalSection(coreCommon.Name)
		routeChanged, err := routing.EnsureProxyRoute(newServerIP.String(), gatewayIP)
		common.Client.MarkOutOffCriticalSection(coreCommon.Name)
		if err != nil {
			return fmt.Errorf("failed to add route for new server: %w", err)
		}
		newRouteChanged = routeChanged
		log.Debugf(coreCommon.Category, "[Darwin] Hot-switch route ready newServer=%s changed=%v elapsed=%s", newServerIP.String(), routeChanged, time.Since(startedAt).Truncate(time.Millisecond))
	}

	if err := device.Open(app.RoutingConfig.RoutingTableID, uplinkIface); err != nil {
		if newRouteChanged {
			common.Client.MarkInCriticalSection(coreCommon.Name)
			if cleanupErr := routing.DeleteProxyRoute(newServerIP.String(), gatewayIP); cleanupErr != nil {
				log.Debugf(coreCommon.Category, "[Darwin] Hot-switch cleanup new route failed after open error: %v", cleanupErr)
			}
			common.Client.MarkOutOffCriticalSection(coreCommon.Name)
		}
		return fmt.Errorf("failed to open new protocol device: %w", err)
	}
	log.Debugf(coreCommon.Category, "[Darwin] Hot-switch ProtocolDevice.Open OK proxy=%s elapsed=%s", device.GetProxyAddr(), time.Since(startedAt).Truncate(time.Millisecond))

	if err := tunnel.SwitchVPNProxy(device.GetProxyAddr()); err != nil {
		_ = device.Close()
		if newRouteChanged {
			common.Client.MarkInCriticalSection(coreCommon.Name)
			if cleanupErr := routing.DeleteProxyRoute(newServerIP.String(), gatewayIP); cleanupErr != nil {
				log.Debugf(coreCommon.Category, "[Darwin] Hot-switch cleanup new route failed after switch error: %v", cleanupErr)
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
			log.Debugf(coreCommon.Category, "[Darwin] Hot-switch old ProtocolDevice.Close failed: %v", err)
		}
	}
	if oldServerIP != "" && oldServerIP != newServerIP.String() {
		common.Client.MarkInCriticalSection(coreCommon.Name)
		if err := routing.DeleteProxyRoute(oldServerIP, gatewayIP); err != nil {
			log.Debugf(coreCommon.Category, "[Darwin] Hot-switch old server route cleanup failed oldServer=%s err=%v", oldServerIP, err)
		}
		common.Client.MarkOutOffCriticalSection(coreCommon.Name)
	}

	log.Debugf(coreCommon.Category, "[Darwin] Hot-switch protocol done oldServer=%s newServer=%s proxy=%s elapsed=%s", oldServerIP, newServerIP.String(), device.GetProxyAddr(), time.Since(startedAt).Truncate(time.Millisecond))
	return nil
}
