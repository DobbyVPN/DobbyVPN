//go:build windows
// +build windows

package internal

import (
	"context"
	"fmt"
	"os"
	"sync"

	"go_client/common"
	"go_client/routing"
	"go_client/tunnel"

	"github.com/jackpal/gateway"
	log "go_client/logger"
	outlineCommon "go_client/outline/common"
)

func add_route(proxyIp string) {
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		panic(err)
	}
	interfaceName, err := routing.FindInterfaceByGateway(gatewayIP.String())
	if err != nil {
		panic(err)
	}
	netInterface, err := routing.GetNetworkInterfaceByIP(interfaceName)
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
	routing.AddOrUpdateProxyRoute(proxyIp, gatewayIP.String(), netInterface.Name)
}

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
	trafficCopyWg := &sync.WaitGroup{}
	defer trafficCopyWg.Wait()

	TunGateway := "10.0.85.1"
	TunDeviceIP := "10.0.85.2"

	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		signalInit(initResult, err)
		return err
	}
	interfaceIP, err := routing.FindInterfaceByGateway(gatewayIP.String())
	if err != nil {
		signalInit(initResult, err)
		return err
	}

	netInterface, err := routing.GetNetworkInterfaceByIP(interfaceIP)
	if err != nil {
		log.Infof("Error: %v", err)
		signalInit(initResult, err)
		return err
	}

	log.Infof("[Routing] Pre-resolving server IP from config...")
	serverIP, err := ResolveServerIPFromConfig(*app.TransportConfig)
	if err != nil {
		err = fmt.Errorf("failed to resolve server IP from config: %w", err)
		signalInit(initResult, err)
		return err
	}
	log.Infof("[Routing] Server IP resolved: %s", serverIP.String())

	if serverIP.String() != "127.0.0.1" {
		log.Infof("[Routing] Adding early route for server %s via %s", serverIP.String(), gatewayIP.String())
		common.Client.MarkInCriticalSection(outlineCommon.Name)
		routing.AddOrUpdateProxyRoute(serverIP.String(), gatewayIP.String(), netInterface.Name)
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
		log.Infof("[Routing] Early server route added successfully")
	} else {
		log.Infof("[Routing] Skipping early route for localhost (Cloak mode)")
	}

	tun, err := NewTunDevice(app.RoutingConfig.TunDeviceName, TunDeviceIP)
	if err != nil {
		err = fmt.Errorf("failed to create tun device: %w", err)
		signalInit(initResult, err)
		return err
	}

	ss, err := NewOutlineDevice(*app.TransportConfig)
	if err != nil {
		_ = tun.Close()
		err = fmt.Errorf("failed to create OutlineDevice: %w", err)
		signalInit(initResult, err)
		return err
	}
	log.Infof("Create Device")

	log.Infof("[Outline] Refreshing Shadowsocks session...")
	if err := ss.Refresh(); err != nil {
		_ = tun.Close()
		_ = ss.Close()
		log.Infof("Failed to refresh OutlineDevice: %v", err)
		err = fmt.Errorf("failed to refresh OutlineDevice: %w", err)
		signalInit(initResult, err)
		return err
	}
	log.Infof("[Outline] Session refreshed successfully")

	log.Infof("[Routing] Looking up TUN interface by IP: %s", TunDeviceIP)
	tunInterface, err := routing.GetNetworkInterfaceByIP(TunDeviceIP)
	if err != nil {
		_ = tun.Close()
		_ = ss.Close()
		log.Infof("Could not find TUN interface: %v", err)
		err = fmt.Errorf("failed to find TUN interface: %w", err)
		signalInit(initResult, err)
		return err
	}
	log.Infof("[Routing] Found TUN interface: %s", tunInterface.Name)

	log.Infof("[Routing] Starting routing configuration:")
	log.Infof("  Server IP:     %s", serverIP.String())
	log.Infof("  Gateway IP:    %s", gatewayIP.String())
	log.Infof("  TUN Interface: %s", tunInterface.Name)
	log.Infof("  Net Interface: %s", netInterface.Name)
	log.Infof("  Tun Gateway:   %s", TunGateway)
	log.Infof("  Tun Device IP: %s", TunDeviceIP)

	common.Client.MarkInCriticalSection(outlineCommon.Name)
	if err := routing.StartRouting(
		serverIP.String(),
		gatewayIP.String(),
		tunInterface.Name,
		netInterface.Name,
		TunGateway,
		TunDeviceIP,
	); err != nil {
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
		_ = tun.Close()
		_ = ss.Close()
		log.Infof("Failed to configure routing: %v", err)
		err = fmt.Errorf("failed to configure routing: %w", err)
		signalInit(initResult, err)
		return err
	}
	common.Client.MarkOutOffCriticalSection(outlineCommon.Name)

	log.Infof("[Routing] Routing successfully configured")

	signalInit(initResult, nil)

	defer func() {
		common.Client.MarkInCriticalSection(outlineCommon.Name)
		log.Infof("[Routing] Cleaning up routes for %s...", serverIP.String())
		routing.StopRouting(serverIP.String(), tunInterface.Name, gatewayIP.String(), netInterface.Name, TunGateway)
		log.Infof("[Routing] Routes cleaned up")
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
	}()

	var closeOnce sync.Once
	closeAll := func() {
		closeOnce.Do(func() {
			log.Infof("[Outline] Closing interfaces")
			_ = tun.Close()
			_ = ss.Close()
		})
	}

	defer closeAll()

	go func() {
		<-ctx.Done()
		closeAll()
		log.Infof("[Outline] Cancel received — closing interfaces")
	}()

	tunnel.StartTransfer(
		tun,

		// ss → tun
		func(buf []byte) (int, error) {
			return ss.Read(buf)
		},

		// tun → ss
		func(b []byte) (int, error) {
			return ss.Write(b)
		},
	)

	<-ctx.Done()

	log.Infof("[Tunnel] Context cancelled, stopping transfer")
	tunnel.StopTransfer()

	log.Infof("Outline/app: received interrupt signal, terminating...\n")
	return nil
}
