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
	outlineCommon "go_module/outline/common"
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
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		signalInit(initResult, err)
		return err
	}

	log.Debugf(Category, "[Network] Default gateway detected: %s", gatewayIP.String())

	log.Debugf(Category, "[Routing] Resolving server IP from config...")
	serverIP, err := ResolveServerIPFromConfig(*app.TransportConfig)
	if err != nil {
		err = fmt.Errorf("failed to resolve server IP from config: %w", err)
		signalInit(initResult, err)
		return err
	}
	log.Debugf(Category, "[Routing] Server IP resolved: %s", serverIP.String())

	if serverIP.String() != "127.0.0.1" {
		log.Debugf(Category, "[Routing] Adding direct route for VPN server %s via gateway %s (bypass VPN)", serverIP.String(), gatewayIP.String())
		common.Client.MarkInCriticalSection(outlineCommon.Name)
		if err := routing.AddProxyRoute(serverIP.String(), gatewayIP.String()); err != nil {
			common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
			err = fmt.Errorf("failed to add early route for server: %w", err)
			signalInit(initResult, err)
			return err
		}
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
		log.Debugf(Category, "[Routing] Direct route for VPN server installed")
	} else {
		log.Debugf(Category, "[Routing] Skipping direct route for localhost (Cloak mode)")
	}

	log.Debugf(Category, "[TUN] Initializing virtual interface (utun)...")

	ss, err := NewOutlineDevice(*app.TransportConfig)
	if err != nil {
		err = fmt.Errorf("failed to create OutlineDevice: %w", err)
		signalInit(initResult, err)
		return err
	}

	log.Infof(Category, "[Outline] OutlineDevice successfully created")

	var closeOnce sync.Once
	closeAll := func() {
		closeOnce.Do(func() {
			log.Debugf(Category, "[Lifecycle] Shutting down VPN components (tun2socks + device)")
			tunnel.StopEngine()
			_ = ss.Close()
		})
	}

	defer closeAll()

	go func() {
		<-ctx.Done()
		closeAll()
		log.Debugf(Category, "[Lifecycle] Context cancelled — closing VPN interfaces")
	}()

	defer func() {
		common.Client.MarkInCriticalSection(outlineCommon.Name)
		log.Debugf(Category, "[Routing] Restoring system routing (removing VPN routes)...")
		routing.StopRouting(serverIP.String(), gatewayIP.String())
		log.Debugf(Category, "[Routing] System default route restored via %s", gatewayIP.String())
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
	}()

	log.Debugf(Category, "[Tunnel] Starting tun2socks engine (darwin/utun mode)...")

	err = tunnel.StartEngine(platform_engine.EngineConfig{
		ProxyAddr:   ss.GetProxyAddr(),
		FD:          -1,
		UplinkIface: "",
	})
	if err != nil {
		signalInit(initResult, err)
		return err
	}

	tunName := platform_engine.LastIface

	log.Debugf(Category, "[Tunnel] tun2socks started, interface: %s", tunName)

	common.Client.MarkInCriticalSection(outlineCommon.Name)

	log.Debugf(Category, "[Routing] Switching default route to TUN interface (%s)", tunName)
	if err := routing.StartRouting(serverIP.String(), gatewayIP.String(), tunName); err != nil {
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
		err = fmt.Errorf("failed to configure routing: %w", err)
		signalInit(initResult, err)
		return err
	}
	log.Debugf(Category, "[Routing] Default route is now redirected to VPN (TUN)")

	log.Debugf(Category, "[Tunnel] VPN dataplane is up, starting traffic handling...")

	ifaceName, idx, err := protected_dialer.GetDefaultInterfaceNameDarwin(gatewayIP)
	if err != nil {
		log.Warnf(Category, "[Darwin-Protect] ERROR: failed to detect default interface for protected sockets: %v", err)
	} else {
		log.Debugf(Category, "[Darwin-Protect] Selected interface for direct traffic: %s (index=%d)", ifaceName, idx)

		protected_dialer.SetDefaultInterface(idx)

		log.Debugf(Category, "[Routing] Adding scoped default route via %s -> %s (for protected traffic bypass)", ifaceName, gatewayIP.String())
		if err := routing.AddScopedDefaultRoute(ifaceName, gatewayIP.String()); err != nil {
			log.Warnf(Category, "[Routing] ERROR: failed to add scoped default route via %s: %v", ifaceName, err)
		} else {
			log.Debugf(Category, "[Routing] Scoped default route installed: interface=%s gateway=%s", ifaceName, gatewayIP.String())
		}
	}

	defer func() {
		if ifaceName != "" {
			log.Debugf(Category, "[Routing] Removing scoped default route for interface %s", ifaceName)
			routing.DeleteScopedDefaultRoute(ifaceName)
		}
	}()

	common.Client.MarkOutOffCriticalSection(outlineCommon.Name)

	log.Infof(Category, "[Lifecycle] VPN initialization completed successfully")

	signalInit(initResult, nil)

	<-ctx.Done()

	log.Infof(Category, "[Lifecycle] Context cancelled — stopping VPN engine")

	return nil
}
