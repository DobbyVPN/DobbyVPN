//go:build windows && !(android || ios)

package internal

import (
	"context"
	"fmt"
	"time"

	"github.com/jackpal/gateway"

	"go_module/common"
	"go_module/log"
	"go_module/routing"
	"go_module/tunnel"
	"go_module/tunnel/platform_engine"
	"go_module/tunnel/protected_dialer"
	xrayCommon "go_module/xray/common"
)

func signalInit(initResult chan<- error, err error) {
	if initResult != nil {
		select {
		case initResult <- err:
		default:
		}
	}
}

func (app App) Run(ctx context.Context, initResult chan<- error) error {
	tunGateway := xrayCommon.TunGateway
	tunDeviceIP := xrayCommon.TunIP

	if app.VlessConfig == nil || *app.VlessConfig == "" {
		err := fmt.Errorf("vless config is required")
		signalInit(initResult, err)
		return err
	}

	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		signalInit(initResult, err)
		return err
	}

	interfaceIP, err := routing.FindInterfaceIPByGateway(gatewayIP.String())
	if err != nil {
		signalInit(initResult, err)
		return err
	}

	netInterface, err := routing.GetNetworkInterfaceByIP(interfaceIP)
	if err != nil {
		err = fmt.Errorf("failed to get network interface by IP %s: %w", interfaceIP, err)
		log.Infof("[Xray][Routing] %v", err)
		signalInit(initResult, err)
		return err
	}

	device, err := NewXrayDevice(*app.VlessConfig, app.RoutingConfig.RoutingTableID, netInterface.Name)
	if err != nil {
		err = fmt.Errorf("failed to create XrayDevice: %w", err)
		signalInit(initResult, err)
		return err
	}
	defer func() { _ = device.Close() }()

	serverIP := device.GetServerIP()

	// protect route to VPN server
	if serverIP != nil && serverIP.String() != "127.0.0.1" {
		log.Infof("[Xray][Routing] Adding early route for server %s via %s", serverIP.String(), gatewayIP.String())
		common.Client.MarkInCriticalSection(xrayCommon.Name)
		routing.AddOrUpdateProxyRoute(serverIP.String(), gatewayIP.String(), netInterface.Name)
		common.Client.MarkOutOffCriticalSection(xrayCommon.Name)
		log.Infof("[Xray][Routing] Early server route added successfully")
	} else {
		log.Infof("[Xray][Routing] Skipping early route for localhost")
	}

	idx, err := protected_dialer.GetDefaultInterfaceIndex()
	if err != nil {
		err = fmt.Errorf("failed to get default interface index: %w", err)
		signalInit(initResult, err)
		return err
	}
	protected_dialer.SetDefaultInterfaceIndex(idx)

	log.Infof("[Xray][Windows] Starting tun2socks in wintun mode")
	log.Infof("[Xray][Windows] Uplink interface: %s", netInterface.Name)
	log.Infof("[Xray][Windows] Proxy addr: %s", device.GetProxyAddr())

	err = tunnel.StartEngine(platform_engine.EngineConfig{
		ProxyAddr:   device.GetProxyAddr(),
		FD:          -1,
		UplinkIface: netInterface.Name,
	})
	if err != nil {
		signalInit(initResult, err)
		return err
	}

	tunInterface, err := routing.WaitForInterfaceByIP(tunDeviceIP, 5*time.Second)
	if err != nil {
		tunnel.StopEngine()
		signalInit(initResult, err)
		return err
	}

	if err := routing.StartRouting(
		serverIP.String(),
		gatewayIP.String(),
		tunInterface.Name,
		netInterface.Name,
		tunGateway,
		tunDeviceIP,
	); err != nil {
		tunnel.StopEngine()
		err = fmt.Errorf("failed to configure routing: %w", err)
		log.Infof("[Xray][Routing] %v", err)
		signalInit(initResult, err)
		return err
	}

	log.Infof("[Xray][Routing] Routing successfully configured")

	signalInit(initResult, nil)

	defer func() {
		common.Client.MarkInCriticalSection(xrayCommon.Name)
		log.Infof("[Xray][Routing] Cleaning up routes for %s...", serverIP.String())
		routing.StopRouting(serverIP.String(), tunInterface.Name, gatewayIP.String(), netInterface.Name, tunGateway)
		common.Client.MarkOutOffCriticalSection(xrayCommon.Name)

		log.Infof("[Xray][Tunnel] Stopping tun2socks engine")
		tunnel.StopEngine()
	}()

	<-ctx.Done()
	log.Infof("[Xray][Tunnel] Context cancelled, shutting down...")
	return nil
}
