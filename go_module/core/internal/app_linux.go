//go:build linux && !(android || ios)
// +build linux,!android,!ios

package internal

import (
	"context"
	"fmt"
	"go_module/core/pkg"
	"go_module/tunnel/platform_engine"
	"go_module/tunnel/protected_dialer"
	"sync"
	"time"

	"github.com/jackpal/gateway"

	"go_module/common"
	coreCommon "go_module/core/common"
	"go_module/log"
	"go_module/routing"
	"go_module/tunnel"
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
	log.Debugf(coreCommon.Category, "[Linux][Init] ===== VPN initialization started =====")
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

	// 1. discover gateway
	log.Debugf(coreCommon.Category, "[Linux][Step 1] Discovering default gateway...")
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		err = fmt.Errorf("failed to discover gateway: %w", err)
		log.Debugf(coreCommon.Category, "[Linux][Step 1][ERROR] %v", err)
		signalInit(initResult, err)
		return err
	}
	log.Debugf(coreCommon.Category, "[Linux][Step 1][OK] Gateway=%s", gatewayIP.String())

	// 2. resolve VPN server IP
	serverIP := app.ProtocolDevice.GetServerIP()
	if serverIP == nil {
		err = fmt.Errorf("server IP is nil")
		signalInit(initResult, err)
		return err
	}
	log.Debugf(coreCommon.Category, "[Routing] Server IP resolved: %s", serverIP.String())

	// 3. detect physical default interface
	log.Debugf(coreCommon.Category, "[Linux][Step 3] Detecting uplink interface...")
	uplinkIface, err := routing.GetDefaultInterfaceNameLinux(gatewayIP.String())
	if err != nil {
		err = fmt.Errorf("failed to detect uplink interface: %w", err)
		log.Debugf(coreCommon.Category, "[Linux][Step 3][ERROR] %v", err)
		signalInit(initResult, err)
		return err
	}
	log.Debugf(coreCommon.Category, "[Linux][Step 3][OK] Uplink interface=%s", uplinkIface)
	protected_dialer.SetDefaultRoute(gatewayIP.String(), uplinkIface, 0)

	// 4. early route
	earlyRouteInstalled := false
	if serverIP.String() != "127.0.0.1" {
		log.Debugf(coreCommon.Category, "[Linux][Step 4] Installing early route → %s via %s dev %s",
			serverIP, gatewayIP, uplinkIface)

		common.Client.MarkInCriticalSection(coreCommon.Name)
		var routeChanged bool
		routeChanged, err = routing.EnsureProxyRoute(serverIP.String(), gatewayIP.String(), uplinkIface)
		if err != nil {
			common.Client.MarkOutOffCriticalSection(coreCommon.Name)
			err = fmt.Errorf("failed to add early route: %w", err)
			log.Debugf(coreCommon.Category, "[Linux][Step 4][ERROR] %v", err)
			signalInit(initResult, err)
			return err
		}
		common.Client.MarkOutOffCriticalSection(coreCommon.Name)

		earlyRouteInstalled = routeChanged
		log.Debugf(coreCommon.Category, "[Linux][Step 4][OK] Early route installed")
	} else {
		log.Debugf(coreCommon.Category, "[Linux][Step 4] Skipped (localhost / Cloak)")
	}

	markedRoutingConfigured := false
	cleanupStartupRoutes := func(reason string) {
		common.Client.MarkInCriticalSection(coreCommon.Name)
		defer common.Client.MarkOutOffCriticalSection(coreCommon.Name)

		if earlyRouteInstalled {
			log.Debugf(coreCommon.Category, "[Linux][Cleanup] Removing early route after %s", reason)
			if cleanupErr := routing.DeleteProxyRoute(serverIP.String(), gatewayIP.String(), uplinkIface); cleanupErr != nil {
				log.Debugf(coreCommon.Category, "[Linux][Cleanup][WARN] Failed to remove early route: %v", cleanupErr)
			}
		}

		if markedRoutingConfigured {
			log.Debugf(coreCommon.Category, "[Linux][Cleanup] Removing marked routing after %s", reason)
			if cleanupErr := routing.CleanupMarkedRouting(
				app.RoutingConfig.RoutingTableID,
				app.RoutingConfig.RoutingTablePriority,
				uplinkIface,
				gatewayIP.String(),
			); cleanupErr != nil {
				log.Debugf(coreCommon.Category, "[Linux][Cleanup][WARN] Failed to remove marked routing: %v", cleanupErr)
			}
		}
	}

	// 5. marked routing
	log.Debugf(coreCommon.Category, "[Linux][Step 5] Setting up policy routing (fwmark=%d table=%d priority=%d)",
		app.RoutingConfig.RoutingTableID,
		app.RoutingConfig.RoutingTableID,
		app.RoutingConfig.RoutingTablePriority,
	)

	common.Client.MarkInCriticalSection(coreCommon.Name)
	if err = routing.SetupMarkedRouting(
		app.RoutingConfig.RoutingTableID,
		app.RoutingConfig.RoutingTablePriority,
		uplinkIface,
		gatewayIP.String(),
	); err != nil {
		common.Client.MarkOutOffCriticalSection(coreCommon.Name)
		err = fmt.Errorf("failed to setup marked routing: %w", err)
		log.Debugf(coreCommon.Category, "[Linux][Step 5][ERROR] %v", err)
		signalInit(initResult, err)
		return err
	}
	common.Client.MarkOutOffCriticalSection(coreCommon.Name)

	markedRoutingConfigured = true
	log.Debugf(coreCommon.Category, "[Linux][Step 5][OK] Policy routing configured")

	// protected sockets
	protected_dialer.SetLinuxSocketMark(app.RoutingConfig.RoutingTableID)

	log.Debugf(coreCommon.Category, "[Linux][Step 5] Protected dialers installed (SO_MARK=%d)", app.RoutingConfig.RoutingTableID)

	// 6. create TUN
	log.Debugf(coreCommon.Category, "[Linux][Step 6] Creating TUN: name=%s ip=%s",
		app.RoutingConfig.TunDeviceName,
		app.RoutingConfig.TunDeviceIP,
	)

	tun, err := newTunDevice(app.RoutingConfig.TunDeviceName, app.RoutingConfig.TunDeviceIP)
	if err != nil {
		cleanupStartupRoutes("TUN creation error")
		err = fmt.Errorf("failed to create TUN device: %w", err)
		log.Debugf(coreCommon.Category, "[Linux][Step 6][ERROR] %v", err)
		signalInit(initResult, err)
		return err
	}

	log.Debugf(coreCommon.Category, "[Linux][Step 6][OK] TUN created: %s", app.RoutingConfig.TunDeviceName)

	// 7. Protocol
	log.Debugf(coreCommon.Category, "[Linux][Step 7] Creating Protocol SOCKS bridge...")
	err = app.ProtocolDevice.Open(app.RoutingConfig.RoutingTableID, uplinkIface)
	if err != nil {
		_ = tun.Close()
		cleanupStartupRoutes("ProtocolDevice error")
		err = fmt.Errorf("failed to create ProtocolDevice: %w", err)
		log.Debugf(coreCommon.Category, "[Linux][Step 7][ERROR] %v", err)
		signalInit(initResult, err)
		return err
	}
	log.Debugf(coreCommon.Category, "[Linux][Step 7][OK] SOCKS5 proxy=%s", app.ProtocolDevice.GetProxyAddr())

	routingStarted := false
	var closeOnce sync.Once
	closeAll := func() {
		closeOnce.Do(func() {
			log.Debugf(coreCommon.Category, "[Linux][Lifecycle] Shutting down...")

			app.mu.Lock()
			currentDevice := app.currentDevice
			currentServerIP := app.serverIP
			if currentDevice == nil {
				currentDevice = app.ProtocolDevice
			}
			if currentServerIP == "" && serverIP != nil {
				currentServerIP = serverIP.String()
			}
			app.currentDevice = nil
			app.running = false
			app.mu.Unlock()

			if routingStarted {
				common.Client.MarkInCriticalSection(coreCommon.Name)
				if err = routing.StopRouting(currentServerIP, gatewayIP.String(), uplinkIface); err != nil {
					log.Debugf(coreCommon.Category, "[Linux][StopRouting][ERROR] %v", err)
				}
				common.Client.MarkOutOffCriticalSection(coreCommon.Name)
			} else {
				log.Debugf(coreCommon.Category, "[Linux][Lifecycle] Routing switch was not completed; skipping StopRouting")
			}

			if err = routing.CleanupMarkedRouting(
				app.RoutingConfig.RoutingTableID,
				app.RoutingConfig.RoutingTablePriority,
				uplinkIface,
				gatewayIP.String(),
			); err != nil {
				log.Debugf(coreCommon.Category, "[Linux][CleanupMarkedRouting][WARN] %v", err)
			}

			tunnel.StopEngine()

			if currentDevice != nil {
				_ = currentDevice.Close()
			}

			_ = tun.Close()

			log.Debugf(coreCommon.Category, "[Linux][Lifecycle] Shutdown complete")
		})
	}

	defer closeAll()

	// 8. fd
	t, ok := tun.(interface{ GetFd() int })
	if !ok {
		err = fmt.Errorf("TUN has no fd")
		log.Debugf(coreCommon.Category, "[Linux][Step 8][ERROR] %v", err)
		signalInit(initResult, err)
		return err
	}
	fd := t.GetFd()
	if fd < 0 {
		err = fmt.Errorf("invalid fd=%d", fd)
		log.Debugf(coreCommon.Category, "[Linux][Step 8][ERROR] %v", err)
		signalInit(initResult, err)
		return err
	}
	log.Debugf(coreCommon.Category, "[Linux][Step 8][OK] fd=%d", fd)

	// 9. tun2socks
	log.Debugf(coreCommon.Category, "[Linux][Step 9] Starting tun2socks (fd=%d proxy=%s)", fd, app.ProtocolDevice.GetProxyAddr())
	err = tunnel.StartEngine(platform_engine.EngineConfig{
		ProxyAddr:   app.ProtocolDevice.GetProxyAddr(),
		FD:          fd,
		UplinkIface: "",
	})
	if err != nil {
		log.Debugf(coreCommon.Category, "Can't start tun2socks: %v", err)
		signalInit(initResult, err)
		return err
	}

	log.Debugf(coreCommon.Category, "[Linux][Step 9][OK] tun2socks started — waiting for readiness...")

	time.Sleep(300 * time.Millisecond)

	// 10. routing switch
	log.Debugf(coreCommon.Category, "[Linux][Step 10] Switching default route → TUN (%s)", app.RoutingConfig.TunDeviceName)

	common.Client.MarkInCriticalSection(coreCommon.Name)
	if err = routing.StartRouting(serverIP.String(), gatewayIP.String(), uplinkIface, app.RoutingConfig.TunDeviceName); err != nil {
		common.Client.MarkOutOffCriticalSection(coreCommon.Name)
		log.Debugf(coreCommon.Category, "[Linux][Step 10][Rollback] Restoring routing after failed VPN route switch")
		common.Client.MarkInCriticalSection(coreCommon.Name)
		if rollbackErr := routing.StopRouting(serverIP.String(), gatewayIP.String(), uplinkIface); rollbackErr != nil {
			log.Debugf(coreCommon.Category, "[Linux][Step 10][Rollback][WARN] Failed to restore routing after route switch error: %v", rollbackErr)
		}
		common.Client.MarkOutOffCriticalSection(coreCommon.Name)
		err = fmt.Errorf("failed to configure routing: %w", err)
		log.Debugf(coreCommon.Category, "[Linux][Step 10][ERROR] %v", err)
		signalInit(initResult, err)
		return err
	}
	common.Client.MarkOutOffCriticalSection(coreCommon.Name)
	routingStarted = true

	app.mu.Lock()
	app.currentDevice = app.ProtocolDevice
	app.gatewayIP = gatewayIP.String()
	app.uplinkIface = uplinkIface
	app.tunIface = app.RoutingConfig.TunDeviceName
	app.serverIP = serverIP.String()
	app.running = true
	app.mu.Unlock()

	log.Debugf(coreCommon.Category, "[Linux][Step 10][OK] Default route switched to VPN")

	log.Debugf(coreCommon.Category, "[Linux][Init] ===== VPN started successfully =====")

	signalInit(initResult, nil)

	<-ctx.Done()

	log.Debugf(coreCommon.Category, "[Tunnel] Stopping engine")
	tunnel.StopEngine()

	log.Debugf(coreCommon.Category, "[Linux][Lifecycle] Context cancelled — stopping engine")
	return nil
}

func (app *App) SwitchProtocolDevice(device pkg.ProtocolDevice) error {
	startedAt := time.Now()
	if device == nil {
		return fmt.Errorf("protocol device is not initialized")
	}
	replacementAdopted := false
	defer func() {
		if replacementAdopted {
			return
		}
		if closeErr := device.Close(); closeErr != nil {
			log.Debugf(coreCommon.Category, "[Linux] Failed to close replacement ProtocolDevice after failed hot-switch: %v", closeErr)
		}
	}()

	if app == nil {
		return fmt.Errorf("core app is not initialized")
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

	log.Debugf(coreCommon.Category, "[Linux] Hot-switch protocol begin oldServer=%s newServer=%s", oldServerIP, newServerIP.String())

	newRouteChanged := false
	if newServerIP.String() != "127.0.0.1" {
		common.Client.MarkInCriticalSection(coreCommon.Name)
		routeChanged, err := routing.EnsureProxyRoute(newServerIP.String(), gatewayIP, uplinkIface)
		common.Client.MarkOutOffCriticalSection(coreCommon.Name)
		if err != nil {
			return fmt.Errorf("failed to add route for new server: %w", err)
		}
		newRouteChanged = routeChanged
		log.Debugf(coreCommon.Category, "[Linux] Hot-switch route ready newServer=%s changed=%v elapsed=%s", newServerIP.String(), routeChanged, time.Since(startedAt).Truncate(time.Millisecond))
	}

	if err := device.Open(app.RoutingConfig.RoutingTableID, uplinkIface); err != nil {
		if newRouteChanged {
			common.Client.MarkInCriticalSection(coreCommon.Name)
			if cleanupErr := routing.DeleteProxyRoute(newServerIP.String(), gatewayIP, uplinkIface); cleanupErr != nil {
				log.Debugf(coreCommon.Category, "[Linux] Hot-switch cleanup new route failed after open error: %v", cleanupErr)
			}
			common.Client.MarkOutOffCriticalSection(coreCommon.Name)
		}
		return fmt.Errorf("failed to open new protocol device: %w", err)
	}
	log.Debugf(coreCommon.Category, "[Linux] Hot-switch ProtocolDevice.Open OK proxy=%s elapsed=%s", device.GetProxyAddr(), time.Since(startedAt).Truncate(time.Millisecond))

	if err := tunnel.SwitchVPNProxy(device.GetProxyAddr()); err != nil {
		if newRouteChanged {
			common.Client.MarkInCriticalSection(coreCommon.Name)
			if cleanupErr := routing.DeleteProxyRoute(newServerIP.String(), gatewayIP, uplinkIface); cleanupErr != nil {
				log.Debugf(coreCommon.Category, "[Linux] Hot-switch cleanup new route failed after switch error: %v", cleanupErr)
			}
			common.Client.MarkOutOffCriticalSection(coreCommon.Name)
		}
		return fmt.Errorf("failed to switch tun2socks proxy: %w", err)
	}

	app.ProtocolDevice = device
	app.currentDevice = device
	app.serverIP = newServerIP.String()
	replacementAdopted = true

	if oldDevice != nil {
		if err := oldDevice.Close(); err != nil {
			log.Debugf(coreCommon.Category, "[Linux] Hot-switch old ProtocolDevice.Close failed: %v", err)
		}
	}
	if oldServerIP != "" && oldServerIP != newServerIP.String() {
		common.Client.MarkInCriticalSection(coreCommon.Name)
		if err := routing.DeleteProxyRoute(oldServerIP, gatewayIP, uplinkIface); err != nil {
			log.Debugf(coreCommon.Category, "[Linux] Hot-switch old server route cleanup failed oldServer=%s err=%v", oldServerIP, err)
		}
		common.Client.MarkOutOffCriticalSection(coreCommon.Name)
	}

	log.Debugf(coreCommon.Category, "[Linux] Hot-switch protocol done oldServer=%s newServer=%s proxy=%s elapsed=%s", oldServerIP, newServerIP.String(), device.GetProxyAddr(), time.Since(startedAt).Truncate(time.Millisecond))
	return nil
}
