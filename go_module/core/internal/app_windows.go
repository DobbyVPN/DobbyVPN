//go:build windows && !(android || ios)
// +build windows,!android,!ios

package internal

import (
	"context"
	"errors"
	"fmt"
	"go_module/tunnel/platform_engine"
	"go_module/tunnel/protected_dialer"
	"sync/atomic"
	"time"

	"go_module/common"
	"go_module/core/pkg"
	"go_module/routing"
	"go_module/tunnel"

	coreCommon "go_module/core/common"
	"go_module/log"

	"github.com/jackpal/gateway"
)

var windowsRunSequence atomic.Uint64

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

func (app *App) Run(ctx context.Context, initResult chan<- error) (runErr error) {
	startedAt := time.Now()
	routePlan := routing.NewPlan(fmt.Sprintf("windows-%d-%d", startedAt.UnixNano(), windowsRunSequence.Add(1)))

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
	var ownedEngine *tunnel.Engine
	protocolOpened := false
	defer func() {
		app.mu.Lock()
		app.currentDevice = nil
		app.running = false
		app.engine = nil
		app.mu.Unlock()

		common.Client.MarkInCriticalSection(coreCommon.Name)
		log.Debugf(coreCommon.Category, "Closing Windows routing plan before stopping tun2socks")
		routeErr := routePlan.Close()
		common.Client.MarkOutOffCriticalSection(coreCommon.Name)

		log.Debugf(coreCommon.Category, "[Tunnel] Stopping tun2socks engine")
		var engineErr error
		if ownedEngine != nil {
			engineErr = ownedEngine.Stop()
		}
		var deviceErr error
		if protocolOpened {
			deviceErr = app.ProtocolDevice.Close()
		}
		cleanupErr := errors.Join(routeErr, engineErr, deviceErr)
		if cleanupErr != nil {
			log.Debugf(coreCommon.Category, "[Windows][Cleanup][ERROR] %v", cleanupErr)
			runErr = errors.Join(runErr, fmt.Errorf("Windows session cleanup: %w", cleanupErr))
		} else {
			log.Debugf(coreCommon.Category, "[Windows][Cleanup] complete=true")
		}
	}()

	stepStartedAt := time.Now()
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		err = fmt.Errorf("failed to discover gateway: %w", err)
		signalInit(initResult, err)
		return err
	}
	log.Debugf(coreCommon.Category, "[Windows] DiscoverGateway ready=true elapsed=%s total=%s", time.Since(stepStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

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
	log.Debugf(coreCommon.Category, "VPN server address resolved elapsed=%s total=%s", time.Since(stepStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	// Protect the VPN server before opening the protocol. The Plan records this
	// exact route, and releases it only if this Run acquired it.
	if serverIP.String() != "127.0.0.1" {
		log.Debugf(coreCommon.Category, "Adding early VPN bypass route")
		common.Client.MarkInCriticalSection(coreCommon.Name)
		stepStartedAt = time.Now()
		var routeChanged bool
		routeChanged, err = routing.AcquireProxyRoute(routePlan, serverIP.String(), gatewayIP.String(), netInterface.Name)
		if err != nil {
			common.Client.MarkOutOffCriticalSection(coreCommon.Name)
			err = fmt.Errorf("failed to add early route for server: %w", err)
			signalInit(initResult, err)
			return err
		}
		common.Client.MarkOutOffCriticalSection(coreCommon.Name)
		log.Debugf(coreCommon.Category, "Early server route added successfully changed=%v elapsed=%s total=%s", routeChanged, time.Since(stepStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))
	} else {
		log.Debugf(coreCommon.Category, "Skipping early route for localhost (Cloak mode)")
	}
	stepStartedAt = time.Now()
	protected_dialer.SetDefaultRoute(gatewayIP.String(), netInterface.Name, netInterface.Index)
	log.Debugf(coreCommon.Category, "[Windows] Default interface index=%d elapsed=%s total=%s", netInterface.Index, time.Since(stepStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	// SOCKS protocol device
	stepStartedAt = time.Now()
	// Open can partially acquire resources before returning an error, so the
	// single owner defer must attempt Close on both success and failure.
	protocolOpened = true
	err = app.ProtocolDevice.Open(app.RoutingConfig.RoutingTableID, netInterface.Name)
	if err != nil {
		err = fmt.Errorf("failed to create ProtocolDevice: %w", err)
		signalInit(initResult, err)
		return err
	}
	log.Debugf(coreCommon.Category, "[Windows] ProtocolDevice.Open OK proxy_ready=true elapsed=%s total=%s", time.Since(stepStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	log.Debugf(coreCommon.Category, "[Windows] Starting tun2socks in wintun mode")
	log.Debugf(coreCommon.Category, "[Windows] Uplink interface: %s", netInterface.Name)
	log.Debugf(coreCommon.Category, "[Windows] Local protocol proxy ready")

	stepStartedAt = time.Now()
	ownedEngine, err = tunnel.StartOwnedEngine(platform_engine.EngineConfig{
		ProxyAddr:   app.ProtocolDevice.GetProxyAddr(),
		FD:          -1,
		UplinkIface: netInterface.Name,
	})
	if err != nil {
		log.Debugf(coreCommon.Category, "Can't start tun2socks: %v", err)
		signalInit(initResult, err)
		return err
	}
	app.mu.Lock()
	app.engine = ownedEngine
	app.mu.Unlock()
	log.Debugf(coreCommon.Category, "[Windows] tunnel.StartEngine OK elapsed=%s total=%s", time.Since(stepStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	stepStartedAt = time.Now()
	tunInterface, err := routing.WaitForInterfaceByIP(cfg.TunDevice, 5*time.Second)
	if err != nil {
		signalInit(initResult, err)
		return err
	}
	expectedInterface := ownedEngine.InterfaceName()
	if expectedInterface == "" || tunInterface.Name != expectedInterface {
		err = fmt.Errorf(
			"interface with TUN address is %q, expected owned adapter %q",
			tunInterface.Name,
			expectedInterface,
		)
		signalInit(initResult, err)
		return err
	}
	log.Debugf(coreCommon.Category, "[Windows] WaitForOwnedInterfaceByIP OK iface=%s elapsed=%s total=%s", tunInterface.Name, time.Since(stepStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	// routing
	common.Client.MarkInCriticalSection(coreCommon.Name)
	stepStartedAt = time.Now()
	if err := routing.ConfigureWindowsRouting(
		routePlan,
		serverIP.String(),
		gatewayIP.String(),
		tunInterface.Name,
		netInterface.Name,
	); err != nil {
		common.Client.MarkOutOffCriticalSection(coreCommon.Name)
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

	<-ctx.Done()

	log.Debugf(coreCommon.Category, "[Tunnel] Context cancelled, shutting down...")
	log.Debugf(coreCommon.Category, "Core/app: received interrupt signal, terminating...")

	return nil
}

func (app *App) SwitchProtocolDevice(device pkg.ProtocolDevice) error {
	_ = app
	_ = device
	// A replacement needs a second independently-owned server bypass lease. The
	// current tunnel API does not retain that Plan, so refuse the transition
	// instead of deleting a route that may predate this session.
	return fmt.Errorf("Windows protocol hot-switch is unavailable while routing leases are session-owned")
}
