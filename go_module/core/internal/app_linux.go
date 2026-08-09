//go:build linux && !(android || ios)
// +build linux,!android,!ios

package internal

import (
	"context"
	"errors"
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

func (app *App) validateRunInputs() error {
	if app.ProtocolDevice == nil {
		return fmt.Errorf("protocol device is not initialized")
	}
	if app.RoutingConfig == nil {
		return fmt.Errorf("routing config is not initialized")
	}
	return nil
}

func (app *App) Run(ctx context.Context, initResult chan<- error) (runErr error) {
	log.Debugf(coreCommon.Category, "[Linux][Init] ===== VPN initialization started =====")
	if err := app.validateRunInputs(); err != nil {
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
	log.Debugf(coreCommon.Category, "[Linux][Step 1][OK] Default gateway discovered")

	// 2. resolve VPN server IP
	serverIP := app.ProtocolDevice.GetServerIP()
	if serverIP == nil {
		err = fmt.Errorf("server IP is nil")
		signalInit(initResult, err)
		return err
	}
	log.Debugf(coreCommon.Category, "[Routing] VPN server address resolved")

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
	routePlan := routing.NewPlan(fmt.Sprintf("%s:%s", app.RoutingConfig.TunDeviceName, serverIP.String()))
	defer func() {
		if cleanupErr := routePlan.Close(); cleanupErr != nil {
			log.Debugf(coreCommon.Category, "[Linux][RoutingPlan][WARN] %v", cleanupErr)
			runErr = errors.Join(runErr, fmt.Errorf("Linux routing cleanup: %w", cleanupErr))
		}
	}()

	// 4. early route
	if serverIP.String() != "127.0.0.1" {
		log.Debugf(coreCommon.Category, "[Linux][Step 4] Installing early VPN bypass route uplink=%s", uplinkIface)

		common.Client.MarkInCriticalSection(coreCommon.Name)
		_, err = routePlan.AcquireLinuxProxyRoute(serverIP.String(), gatewayIP.String(), uplinkIface)
		if err != nil {
			common.Client.MarkOutOffCriticalSection(coreCommon.Name)
			err = fmt.Errorf("failed to add early route: %w", err)
			log.Debugf(coreCommon.Category, "[Linux][Step 4][ERROR] %v", err)
			signalInit(initResult, err)
			return err
		}
		common.Client.MarkOutOffCriticalSection(coreCommon.Name)

		log.Debugf(coreCommon.Category, "[Linux][Step 4][OK] Early route installed")
	} else {
		log.Debugf(coreCommon.Category, "[Linux][Step 4] Skipped (localhost / Cloak)")
	}

	// 5. marked routing
	log.Debugf(coreCommon.Category, "[Linux][Step 5] Setting up policy routing (fwmark=%d table=%d priority=%d)",
		app.RoutingConfig.RoutingTableID,
		app.RoutingConfig.RoutingTableID,
		app.RoutingConfig.RoutingTablePriority,
	)

	common.Client.MarkInCriticalSection(coreCommon.Name)
	if err = routePlan.AcquireLinuxMarkedRouting(
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

	log.Debugf(coreCommon.Category, "[Linux][Step 5][OK] Policy routing configured")

	// protected sockets
	protected_dialer.SetLinuxSocketMark(app.RoutingConfig.RoutingTableID)
	defer protected_dialer.SetLinuxSocketMark(0)

	log.Debugf(coreCommon.Category, "[Linux][Step 5] Protected dialers installed (SO_MARK=%d)", app.RoutingConfig.RoutingTableID)

	// 6. create TUN
	log.Debugf(coreCommon.Category, "[Linux][Step 6] Creating TUN: name=%s ip=%s",
		app.RoutingConfig.TunDeviceName,
		app.RoutingConfig.TunDeviceIP,
	)

	tun, err := newTunDevice(app.RoutingConfig.TunDeviceName, app.RoutingConfig.TunDeviceIP)
	if err != nil {
		err = fmt.Errorf("failed to create TUN device: %w", err)
		log.Debugf(coreCommon.Category, "[Linux][Step 6][ERROR] %v", err)
		signalInit(initResult, err)
		return err
	}

	log.Debugf(coreCommon.Category, "[Linux][Step 6][OK] TUN created: %s", app.RoutingConfig.TunDeviceName)

	var ownedEngine *tunnel.Engine
	protocolOpened := false
	var cleanupErr error
	var closeOnce sync.Once
	closeAll := func() error {
		closeOnce.Do(func() {
			log.Debugf(coreCommon.Category, "[Linux][Lifecycle] Shutting down...")

			app.mu.Lock()
			currentDevice := app.currentDevice
			if currentDevice == nil && protocolOpened {
				currentDevice = app.ProtocolDevice
			}
			app.currentDevice = nil
			app.running = false
			ownedEngine = app.engine
			app.engine = nil
			app.mu.Unlock()

			common.Client.MarkInCriticalSection(coreCommon.Name)
			routeErr := routePlan.Close()
			common.Client.MarkOutOffCriticalSection(coreCommon.Name)

			var engineErr error
			if ownedEngine != nil {
				engineErr = ownedEngine.Stop()
			}
			var deviceErr error
			if currentDevice != nil {
				deviceErr = currentDevice.Close()
			}
			tunErr := tun.Close()
			cleanupErr = errors.Join(routeErr, engineErr, deviceErr, tunErr)
			if cleanupErr != nil {
				log.Debugf(coreCommon.Category, "[Linux][Cleanup][ERROR] %v", cleanupErr)
			} else {
				log.Debugf(coreCommon.Category, "[Linux][Lifecycle] Shutdown complete")
			}
		})
		return cleanupErr
	}
	defer func() {
		if err := closeAll(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("Linux session cleanup: %w", err))
		}
	}()

	// 7. Protocol
	log.Debugf(coreCommon.Category, "[Linux][Step 7] Creating Protocol SOCKS bridge...")
	// Open may partially allocate protocol resources before returning an error.
	protocolOpened = true
	err = app.ProtocolDevice.Open(app.RoutingConfig.RoutingTableID, uplinkIface)
	if err != nil {
		err = fmt.Errorf("failed to create ProtocolDevice: %w", err)
		log.Debugf(coreCommon.Category, "[Linux][Step 7][ERROR] %v", err)
		signalInit(initResult, err)
		return err
	}
	log.Debugf(coreCommon.Category, "[Linux][Step 7][OK] Protocol SOCKS bridge ready")

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
	log.Debugf(coreCommon.Category, "[Linux][Step 9] Starting tun2socks fd=%d proxy_ready=true", fd)
	ownedEngine, err = tunnel.StartOwnedEngine(platform_engine.EngineConfig{
		ProxyAddr:   app.ProtocolDevice.GetProxyAddr(),
		FD:          fd,
		UplinkIface: "",
	})
	if err != nil {
		log.Debugf(coreCommon.Category, "Can't start tun2socks: %v", err)
		signalInit(initResult, err)
		return err
	}
	app.mu.Lock()
	app.engine = ownedEngine
	app.mu.Unlock()

	log.Debugf(coreCommon.Category, "[Linux][Step 9][OK] tun2socks started — waiting for readiness...")

	time.Sleep(300 * time.Millisecond)

	// 10. routing switch
	log.Debugf(coreCommon.Category, "[Linux][Step 10] Switching default route → TUN (%s)", app.RoutingConfig.TunDeviceName)

	common.Client.MarkInCriticalSection(coreCommon.Name)
	if _, err = routePlan.AcquireLinuxTunnelDefault(app.RoutingConfig.TunDeviceName); err == nil {
		err = routePlan.AcquireLinuxIPv6Block()
	}
	if err != nil {
		common.Client.MarkOutOffCriticalSection(coreCommon.Name)
		err = fmt.Errorf("failed to configure routing: %w", err)
		log.Debugf(coreCommon.Category, "[Linux][Step 10][ERROR] %v", err)
		signalInit(initResult, err)
		return err
	}
	common.Client.MarkOutOffCriticalSection(coreCommon.Name)

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

	log.Debugf(coreCommon.Category, "[Linux][Lifecycle] Context cancelled — stopping engine")
	return nil
}

func (app *App) SwitchProtocolDevice(device pkg.ProtocolDevice) error {
	_ = app
	if device != nil {
		if closeErr := device.Close(); closeErr != nil {
			log.Debugf(coreCommon.Category, "[Linux][Lifecycle] replacement device close after rejected switch failed: %v", closeErr)
		}
	}
	// A replacement would require a second server bypass lease while the
	// existing TUN, engine, and routing plan remain live. Refuse it rather than
	// partially changing a generation or deleting a route we do not own.
	return fmt.Errorf("linux protocol hot-switch is unavailable; stop the active session before starting another profile")
}
