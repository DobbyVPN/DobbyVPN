//go:build windows && !(android || ios)
// +build windows,!android,!ios

package internal

import (
	"context"
	"core/tunnel/platform_engine"
	"core/tunnel/protected_dialer"
	"fmt"
	"sync/atomic"
	"time"

	"core/routing"
	"core/tunnel"

	"core/log"
)

var windowsRunSequence atomic.Uint64

func (app *App) Run(ctx context.Context, initResult chan<- error) (runErr error) {
	defer func() { runErr = app.finishCleanup(ctx, runErr) }()
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

	var ownedEngine *tunnel.Engine
	protocolOpened := false
	app.setCleanup(routePlan.Close,
		func(ctx context.Context) error {
			if ownedEngine != nil {
				return ownedEngine.Stop(ctx)
			}
			return nil
		},
		func(context.Context) error {
			if protocolOpened {
				return app.ProtocolDevice.Close()
			}
			return nil
		},
		func(context.Context) error {
			protected_dialer.ResetDefaultRoute()
			return nil
		})

	stepStartedAt := time.Now()
	gatewayIP, netInterface, err := routing.DiscoverWindowsDefaultRoute()
	if err != nil {
		signalInit(initResult, err)
		return err
	}
	log.Debugf(Category, "[Windows] default route gateway=%s interface=%s index=%d elapsed=%s", gatewayIP, netInterface.Name, netInterface.Index, time.Since(stepStartedAt))

	stepStartedAt = time.Now()
	serverIP := app.ProtocolDevice.GetServerIP()
	if serverIP == nil {
		err = fmt.Errorf("server IP is nil")
		signalInit(initResult, err)
		return err
	}
	log.Debugf(Category, "VPN server address resolved elapsed=%s total=%s", time.Since(stepStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	// Protect the VPN server before opening the protocol. The Plan records this
	// exact route, and releases it only if this Run acquired it.
	if serverIP.String() != "127.0.0.1" {
		log.Debugf(Category, "Adding early VPN bypass route")
		stepStartedAt = time.Now()
		var routeChanged bool
		routeChanged, err = routing.AcquireProxyRoute(routePlan, serverIP.String(), gatewayIP.String(), netInterface.Name)
		if err != nil {
			err = fmt.Errorf("failed to add early route for server: %w", err)
			signalInit(initResult, err)
			return err
		}
		log.Debugf(Category, "Early server route added successfully changed=%v elapsed=%s total=%s", routeChanged, time.Since(stepStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))
	} else {
		log.Debugf(Category, "Skipping early route for localhost endpoint")
	}
	stepStartedAt = time.Now()
	protected_dialer.SetDefaultRoute(gatewayIP.String(), netInterface.Name, netInterface.Index)
	log.Debugf(Category, "[Windows] Default interface index=%d elapsed=%s total=%s", netInterface.Index, time.Since(stepStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

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
	log.Debugf(Category, "[Windows] ProtocolDevice.Open OK proxy_ready=true elapsed=%s total=%s", time.Since(stepStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	log.Debugf(Category, "[Windows] Starting tun2socks in wintun mode")
	log.Debugf(Category, "[Windows] Uplink interface: %s", netInterface.Name)
	log.Debugf(Category, "[Windows] Local protocol proxy ready")

	stepStartedAt = time.Now()
	ownedEngine, err = tunnel.StartOwnedEngine(platform_engine.EngineConfig{
		ProxyAddr:   app.ProtocolDevice.GetProxyAddr(),
		FD:          -1,
		UplinkIface: netInterface.Name,
	}, app.DNSCache, app.BypassPolicy)
	if err != nil {
		log.Errorf(Category, "Can't start tun2socks: %v", err)
		signalInit(initResult, err)
		return err
	}
	log.Debugf(Category, "[Windows] tunnel.StartOwnedEngine OK elapsed=%s total=%s", time.Since(stepStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	stepStartedAt = time.Now()
	tunInterface, err := routing.WaitForInterfaceName(ownedEngine.InterfaceName(), 5*time.Second)
	if err != nil {
		signalInit(initResult, err)
		return err
	}

	log.Debugf(Category, "[Windows] WaitForOwnedInterfaceByIP OK iface=%s elapsed=%s total=%s", tunInterface.Name, time.Since(stepStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	// routing
	stepStartedAt = time.Now()
	if err := routing.ConfigureWindowsRouting(
		routePlan,
		serverIP.String(),
		gatewayIP.String(),
		tunInterface.Name,
		netInterface.Name,
	); err != nil {
		err = fmt.Errorf("failed to configure routing: %w", err)
		log.Debugf(Category, "%v", err)
		signalInit(initResult, err)
		return err
	}

	log.Debugf(Category, "Routing successfully configured elapsed=%s total=%s", time.Since(stepStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	// Signal successful initialization - connection is ready
	log.Debugf(Category, "[Windows] App initialization ready total=%s", time.Since(startedAt).Truncate(time.Millisecond))
	signalInit(initResult, nil)

	<-ctx.Done()

	log.Debugf(Category, "[Tunnel] Context cancelled, shutting down...")
	log.Debugf(Category, "Runtime: received interrupt signal, terminating...")

	return nil
}
