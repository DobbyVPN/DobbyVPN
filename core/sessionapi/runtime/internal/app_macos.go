//go:build darwin && !(android || ios)
// +build darwin,!android,!ios

package internal

import (
	"context"
	"core/log"
	"core/tunnel/platform_engine"
	"core/tunnel/protected_dialer"
	"fmt"
	"time"

	"core/routing"
	"core/tunnel"

	"github.com/jackpal/gateway"
)

func (app *App) Run(ctx context.Context, initResult chan<- error) (runErr error) {
	defer func() { runErr = app.finishCleanup(ctx, runErr) }()
	log.Debugf(Category, "[Darwin][Init] VPN initialization started")
	defer protected_dialer.ResetDefaultRoute()
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

	log.Debugf(Category, "[Network] Default gateway detected")

	serverIP := app.ProtocolDevice.GetServerIP()
	if serverIP == nil {
		err = fmt.Errorf("server IP is nil")
		signalInit(initResult, err)
		return err
	}
	log.Debugf(Category, "[Routing] VPN server address resolved")

	ifaceName, idx, err := protected_dialer.GetDefaultInterfaceNameDarwin(gatewayIP)
	if err != nil {
		log.Debugf(Category, "[Darwin-Protect] ERROR: failed to detect default interface for protected sockets: %v", err)
	} else {
		log.Debugf(Category, "[Darwin-Protect] Selected interface for direct traffic: %s (index=%d)", ifaceName, idx)
		protected_dialer.SetDefaultRoute(gatewayIP.String(), ifaceName, idx)
	}

	routePlan := routing.NewPlan(fmt.Sprintf("darwin:%p", app))
	var ownedEngine *tunnel.Engine
	tunName := ""
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
		})

	if serverIP.String() != "127.0.0.1" {
		log.Debugf(Category, "[Darwin][Routing] acquiring direct VPN bypass route")
		_, err = routePlan.AcquireMacOSProxyRoute(ctx, serverIP.String(), gatewayIP.String(), ifaceName)
		if err != nil {
			err = fmt.Errorf("failed to acquire server bypass route: %w", err)
			signalInit(initResult, err)
			return err
		}
	} else {
		log.Debugf(Category, "[Darwin][Routing] loopback server bypass not required")
	}

	log.Debugf(Category, "[Darwin][Protocol] opening protocol SOCKS bridge")
	// Open may partially allocate protocol resources before returning an error.
	protocolOpened = true
	err = app.ProtocolDevice.Open(app.RoutingConfig.RoutingTableID, ifaceName)
	if err != nil {
		err = fmt.Errorf("failed to create ProtocolDevice: %w", err)
		signalInit(initResult, err)
		return err
	}
	log.Debugf(Category, "[Darwin][Protocol] protocol SOCKS bridge ready")

	log.Debugf(Category, "[Darwin][Tunnel] starting tun2socks engine")

	ownedEngine, err = tunnel.StartOwnedEngine(platform_engine.EngineConfig{
		ProxyAddr:   app.ProtocolDevice.GetProxyAddr(),
		FD:          -1,
		UplinkIface: "",
	}, app.DNSCache, app.BypassPolicy)
	if err != nil {
		signalInit(initResult, err)
		return err
	}
	tunName = ownedEngine.InterfaceName()

	if tunName == "" {
		err = fmt.Errorf("tun2socks did not report a TUN interface")
		signalInit(initResult, err)
		return err
	}
	log.Debugf(Category, "[Darwin][Tunnel] tun2socks engine ready interface=%s", tunName)

	err = routePlan.AcquireMacOSIPv4Default(ctx, tunName)
	if err == nil {
		err = routePlan.AcquireMacOSIPv6Block(ctx, tunName)
	}
	if err != nil {
		err = fmt.Errorf("failed to acquire generation-owned routing: %w", err)
		signalInit(initResult, err)
		return err
	}
	log.Debugf(Category, "[Darwin][Routing] generation-owned IPv4, IPv6, and protected routes ready")

	log.Debugf(Category, "[Darwin][Lifecycle] VPN initialization completed successfully")

	signalInit(initResult, nil)

	routeRepair := time.NewTicker(time.Second)
	defer routeRepair.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Debugf(Category, "[Darwin][Lifecycle] context cancelled — stopping generation")
			return nil
		case <-routeRepair.C:
			repaired, repairErr := routePlan.Repair()
			if repairErr != nil {
				return fmt.Errorf("repair macOS session routing: %w", repairErr)
			}
			if repaired {
				log.Debugf(Category, "[Darwin][Routing] restored routes removed during physical-uplink transition")
			}
		}
	}
}
