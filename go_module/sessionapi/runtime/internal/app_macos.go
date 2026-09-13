//go:build darwin && !(android || ios)
// +build darwin,!android,!ios

package internal

import (
	"context"
	"errors"
	"fmt"
	"go_module/log"
	"go_module/tunnel/platform_engine"
	"go_module/tunnel/protected_dialer"
	"sync"
	"time"

	"go_module/routing"
	"go_module/tunnel"

	"github.com/jackpal/gateway"
)

func (app *App) Run(ctx context.Context, initResult chan<- error) (runErr error) {
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
	var closeOnce sync.Once
	var cleanupErr error
	tunName := ""
	protocolOpened := false
	closeAll := func() error {
		closeOnce.Do(func() {
			log.Debugf(Category, "[Darwin][Lifecycle] stopping generation-owned resources")
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
			// Remove the session routes while the owned utun still exists, then
			// stop the engine and protocol device.
			routeErr := routePlan.Close()
			var engineErr error
			if ownedEngine != nil {
				engineErr = ownedEngine.Stop()
			}
			var deviceErr error
			if currentDevice != nil {
				deviceErr = currentDevice.Close()
			}
			cleanupErr = errors.Join(routeErr, engineErr, deviceErr)
			if cleanupErr != nil {
				log.Debugf(Category, "[Darwin][Cleanup][ERROR] %v", cleanupErr)
			} else {
				log.Debugf(Category, "[Darwin][Lifecycle] generation cleanup complete")
			}
		})
		return cleanupErr
	}

	defer func() {
		if err := closeAll(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("macOS session cleanup: %w", err))
		}
	}()

	if serverIP.String() != "127.0.0.1" {
		log.Debugf(Category, "[Darwin][Routing] acquiring direct VPN bypass route")
		_, err = routePlan.AcquireMacOSProxyRoute(serverIP.String(), gatewayIP.String())
		if err != nil {
			err = fmt.Errorf("failed to acquire server bypass route: %w", err)
			signalInit(initResult, err)
			return err
		}
	} else {
		log.Debugf(Category, "[Darwin][Routing] loopback server bypass not required")
	}

	log.Debugf(Category, "[Darwin][Protocol] opening protocol SOCKS bridge")
	err = app.ProtocolDevice.Open(app.RoutingConfig.RoutingTableID, ifaceName)
	if err != nil {
		err = fmt.Errorf("failed to create ProtocolDevice: %w", err)
		signalInit(initResult, err)
		return err
	}
	protocolOpened = true
	log.Debugf(Category, "[Darwin][Protocol] protocol SOCKS bridge ready")

	log.Debugf(Category, "[Darwin][Tunnel] starting tun2socks engine")

	ownedEngine, err = tunnel.StartOwnedEngine(platform_engine.EngineConfig{
		ProxyAddr:   app.ProtocolDevice.GetProxyAddr(),
		FD:          -1,
		UplinkIface: "",
	})
	if err != nil {
		signalInit(initResult, err)
		return err
	}
	app.mu.Lock()
	app.engine = ownedEngine
	app.mu.Unlock()

	tunName = platform_engine.LastIface

	if tunName == "" {
		err = fmt.Errorf("tun2socks did not report a TUN interface")
		signalInit(initResult, err)
		return err
	}
	log.Debugf(Category, "[Darwin][Tunnel] tun2socks engine ready interface=%s", tunName)

	err = routePlan.AcquireMacOSIPv4Default(tunName)
	if err == nil {
		err = routePlan.AcquireMacOSIPv6Block(tunName)
	}
	if err != nil {
		err = fmt.Errorf("failed to acquire generation-owned routing: %w", err)
		signalInit(initResult, err)
		return err
	}
	log.Debugf(Category, "[Darwin][Routing] generation-owned IPv4, IPv6, and protected routes ready")

	app.mu.Lock()
	app.currentDevice = app.ProtocolDevice
	app.gatewayIP = gatewayIP.String()
	app.uplinkIface = ifaceName
	app.tunIface = tunName
	app.serverIP = serverIP.String()
	app.running = true
	app.mu.Unlock()

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
			repaired, repairErr := routing.RepairMacOSSessionRoutes(
				serverIP.String(), gatewayIP.String(), tunName, ifaceName,
			)
			if repairErr != nil {
				return fmt.Errorf("repair macOS session routing: %w", repairErr)
			}
			if repaired {
				log.Debugf(Category, "[Darwin][Routing] restored routes removed during physical-uplink transition")
			}
		}
	}
}
