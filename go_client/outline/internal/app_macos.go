//go:build darwin
// +build darwin

package internal

import (
	"context"
	"fmt"
	"go_client/log"
	"sync"

	"go_client/common"
	outlineCommon "go_client/outline/common"
	"go_client/routing"
	"go_client/tunnel"

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

	log.Infof("[Network] Default gateway detected: %s", gatewayIP.String())

	log.Infof("[Routing] Resolving server IP from config...")
	serverIP, err := ResolveServerIPFromConfig(*app.TransportConfig)
	if err != nil {
		err = fmt.Errorf("failed to resolve server IP from config: %w", err)
		signalInit(initResult, err)
		return err
	}
	log.Infof("[Routing] Server IP resolved: %s", serverIP.String())

	if serverIP.String() != "127.0.0.1" {
		log.Infof("[Routing] Adding direct route for VPN server %s via gateway %s (bypass VPN)", serverIP.String(), gatewayIP.String())
		common.Client.MarkInCriticalSection(outlineCommon.Name)
		if err := routing.AddProxyRoute(serverIP.String(), gatewayIP.String()); err != nil {
			common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
			err = fmt.Errorf("failed to add early route for server: %w", err)
			signalInit(initResult, err)
			return err
		}
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
		log.Infof("[Routing] Direct route for VPN server installed")
	} else {
		log.Infof("[Routing] Skipping direct route for localhost (Cloak mode)")
	}

	log.Infof("[TUN] Initializing virtual interface (utun)...")

	ss, err := NewOutlineDevice(*app.TransportConfig)
	if err != nil {
		err = fmt.Errorf("failed to create OutlineDevice: %w", err)
		signalInit(initResult, err)
		return err
	}

	log.Infof("[Outline] OutlineDevice successfully created")

	var closeOnce sync.Once
	closeAll := func() {
		closeOnce.Do(func() {
			log.Infof("[Lifecycle] Shutting down VPN components (tun2socks + device)")
			tunnel.StopEngine()
			_ = ss.Close()
		})
	}

	defer closeAll()

	go func() {
		<-ctx.Done()
		closeAll()
		log.Infof("[Lifecycle] Context cancelled — closing VPN interfaces")
	}()

	defer func() {
		common.Client.MarkInCriticalSection(outlineCommon.Name)
		log.Infof("[Routing] Restoring system routing (removing VPN routes)...")
		routing.StopRouting(serverIP.String(), gatewayIP.String())
		log.Infof("[Routing] System default route restored via %s", gatewayIP.String())
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
	}()

	log.Infof("[Tunnel] Starting tun2socks engine (darwin/utun mode)...")

	tunnel.CustomProtectedDialer = tunnel.DialContextWithProtect
	tunnel.CustomProtectedPacketDialer = tunnel.DialUDPWithProtect

	tunName, err := tunnel.StartEngineDarwin(ss.GetProxyAddr())
	if err != nil {
		signalInit(initResult, err)
		return err
	}

	log.Infof("[Tunnel] tun2socks started, interface: %s", tunName)

	common.Client.MarkInCriticalSection(outlineCommon.Name)

	log.Infof("[Routing] Switching default route to TUN interface (%s)", tunName)
	if err := routing.StartRouting(serverIP.String(), gatewayIP.String(), tunName); err != nil {
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
		err = fmt.Errorf("failed to configure routing: %w", err)
		signalInit(initResult, err)
		return err
	}
	log.Infof("[Routing] Default route is now redirected to VPN (TUN)")

	log.Infof("[Tunnel] VPN dataplane is up, starting traffic handling...")

	ifaceName, idx, err := tunnel.GetDefaultInterfaceNameDarwin()
	if err != nil {
		log.Infof("[Darwin-Protect] ERROR: failed to detect default interface for protected sockets: %v", err)
	} else {
		log.Infof("[Darwin-Protect] Selected interface for direct traffic: %s (index=%d)", ifaceName, idx)

		tunnel.SetDefaultInterface(idx)

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

	common.Client.MarkOutOffCriticalSection(outlineCommon.Name)

	log.Infof("[Lifecycle] VPN initialization completed successfully")

	signalInit(initResult, nil)

	<-ctx.Done()

	log.Infof("[Lifecycle] Context cancelled — stopping VPN engine")

	return nil
}
