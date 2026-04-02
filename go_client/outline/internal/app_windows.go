//go:build windows
// +build windows

package internal

import (
	"context"
	"fmt"
	"go_client/tunnel/protected_dialer"
	"time"

	"go_client/common"
	"go_client/routing"
	"go_client/tunnel"

	"github.com/jackpal/gateway"
	"go_client/log"
	outlineCommon "go_client/outline/common"
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

func (app App) Run(ctx context.Context, initResult chan<- error) error {

	tunGateway := "10.0.85.1"
	tunDeviceIP := "10.0.85.2"

	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		signalInit(initResult, err)
		return err
	}

	interfaceName, err := routing.FindInterfaceIPByGateway(gatewayIP.String())
	if err != nil {
		signalInit(initResult, err)
		return err
	}

	netInterface, err := routing.GetNetworkInterfaceByIP(interfaceName)
	if err != nil {
		err = fmt.Errorf("failed to get network interface by IP %s: %w", interfaceName, err)
		log.Infof("[Routing] %v", err)
		signalInit(initResult, err)
		return err
	}

	serverIP, err := ResolveServerIPFromConfig(*app.TransportConfig)
	if err != nil {
		signalInit(initResult, err)
		return err
	}

	// protect route to VPN server
	if serverIP.String() != "127.0.0.1" {
		log.Infof("[Routing] Adding early route for server %s via %s", serverIP.String(), gatewayIP.String())
		common.Client.MarkInCriticalSection(outlineCommon.Name)
		routing.AddOrUpdateProxyRoute(serverIP.String(), gatewayIP.String(), netInterface.Name)
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
		log.Infof("[Routing] Early server route added successfully")
	} else {
		log.Infof("[Routing] Skipping early route for localhost (Cloak mode)")
	}

	// SOCKS (Outline)
	ss, err := NewOutlineDevice(*app.TransportConfig)
	if err != nil {
		err = fmt.Errorf("failed to create OutlineDevice: %w", err)
		signalInit(initResult, err)
		return err
	}
	defer ss.Close()

	log.Infof("[Windows] Starting tun2socks in wintun mode")
	log.Infof("[Windows] Uplink interface: %s", netInterface.Name)
	log.Infof("[Windows] Proxy addr: %s", ss.GetProxyAddr())

	idx, err := protected_dialer.GetDefaultInterfaceIndex()
	if err != nil {
		err = fmt.Errorf("failed to get default interface index: %w", err)
		signalInit(initResult, err)
		return err
	}
	protected_dialer.SetDefaultInterfaceIndex(idx)

	tunnel.StartEngineWindows(
		ss.GetProxyAddr(),
		netInterface.Name,
	)

	tunInterface, err := routing.WaitForInterfaceByIP(tunDeviceIP, 5*time.Second)
	if err != nil {
		tunnel.StopEngine()
		signalInit(initResult, err)
		return err
	}

	// routing
	if err := routing.StartRouting(
		serverIP.String(),
		gatewayIP.String(),
		tunInterface.Name,
		netInterface.Name,
		tunGateway,
		tunDeviceIP,
	); err != nil {
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
		tunnel.StopEngine()
		err = fmt.Errorf("failed to configure routing: %w", err)
		log.Infof("[Routing] %v", err)
		signalInit(initResult, err)
		return err
	}
	common.Client.MarkOutOffCriticalSection(outlineCommon.Name)

	log.Infof("[Routing] Routing successfully configured")

	// Signal successful initialization - connection is ready
	signalInit(initResult, nil)

	defer func() {
		common.Client.MarkInCriticalSection(outlineCommon.Name)
		log.Infof("[Routing] Cleaning up routes for %s...", serverIP.String())
		routing.StopRouting(serverIP.String(), tunInterface.Name, gatewayIP.String(), netInterface.Name, tunGateway)
		log.Infof("[Routing] Routes cleaned up")
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)

		log.Infof("[Tunnel] Stopping tun2socks engine")
		tunnel.StopEngine()
	}()

	<-ctx.Done()

	log.Infof("[Tunnel] Context cancelled, shutting down...")
	log.Infof("Outline/app: received interrupt signal, terminating...")

	return nil
}
