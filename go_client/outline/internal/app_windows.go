//go:build windows
// +build windows

package internal

import (
	"context"
	"fmt"

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

	// Если потом захочешь вернуть конфиг:
	// tunDeviceIP := app.RoutingConfig.TunDeviceIP
	// tunGatewayCIDR := app.RoutingConfig.TunGatewayCIDR
	// tunGateway := strings.Split(tunGatewayCIDR, "/")[0]

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

	log.Infof("[Routing] Pre-resolving server IP from config...")
	serverIP, err := ResolveServerIPFromConfig(*app.TransportConfig)
	if err != nil {
		err = fmt.Errorf("failed to resolve server IP from config: %w", err)
		signalInit(initResult, err)
		return err
	}
	log.Infof("[Routing] Server IP resolved: %s", serverIP.String())

	// Очень важно заранее защитить маршрут до самого VPN-сервера,
	// чтобы после поднятия туннеля трафик к нему не ушёл в сам туннель.
	if serverIP.String() != "127.0.0.1" {
		log.Infof("[Routing] Adding early route for server %s via %s", serverIP.String(), gatewayIP.String())
		common.Client.MarkInCriticalSection(outlineCommon.Name)
		routing.AddOrUpdateProxyRoute(serverIP.String(), gatewayIP.String(), netInterface.Name)
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
		log.Infof("[Routing] Early server route added successfully")
	} else {
		log.Infof("[Routing] Skipping early route for localhost (Cloak mode)")
	}

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

	// Запоминаем индекс дефолтного интерфейса до старта движка.
	// Это нужно для protect dialer'а.
	idx, err := tunnel.GetDefaultInterfaceIndex()
	if err != nil {
		err = fmt.Errorf("failed to get default interface index: %w", err)
		signalInit(initResult, err)
		return err
	}
	tunnel.SetDefaultInterfaceIndex(idx)

	// Включаем protect dialer для direct/bypass соединений.
	tunnel.CustomProtectedDialer = tunnel.DialContextWithProtect
	tunnel.CustomProtectedPacketDialer = tunnel.DialUDPWithProtect

	// Новый desktop-путь: tun2socks сам создаёт Wintun, сам поднимает dataplane,
	// а внутри StartEngineDesktop уже назначаются IP и DNS на интерфейс.
	tunnel.StartEngineDesktop(
		ss.GetProxyAddr(),
		netInterface.Name,
	)

	// После StartEngineDesktop интерфейс уже должен существовать и иметь IP 10.0.85.2.
	log.Infof("[Routing] Looking up Wintun interface by IP: %s", tunDeviceIP)
	tunInterface, err := routing.GetNetworkInterfaceByIP(tunDeviceIP)
	if err != nil {
		err = fmt.Errorf("failed to find Wintun interface by IP %s: %w", tunDeviceIP, err)
		log.Infof("[Routing] %v", err)
		tunnel.StopEngine()
		signalInit(initResult, err)
		return err
	}
	log.Infof("[Routing] Found Wintun interface: %s (HWAddr=%s)", tunInterface.Name, tunInterface.HardwareAddr)

	dst := tunInterface.HardwareAddr
	src := make([]byte, len(dst))
	copy(src, dst)
	if len(src) > 2 {
		src[2] += 2
	}
	log.Infof("[Routing] Generated spoofed MAC: original=%s new=%v", tunInterface.HardwareAddr, src)

	log.Infof("[Routing] Starting routing configuration:")
	log.Infof("  Server IP:     %s", serverIP.String())
	log.Infof("  Gateway IP:    %s", gatewayIP.String())
	log.Infof("  TUN Interface: %s", tunInterface.Name)
	log.Infof("  TUN MAC:       %s", tunInterface.HardwareAddr.String())
	log.Infof("  Net Interface: %s", netInterface.Name)
	log.Infof("  Tun Gateway:   %s", tunGateway)
	log.Infof("  Tun Device IP: %s", tunDeviceIP)

	common.Client.MarkInCriticalSection(outlineCommon.Name)
	if err := routing.StartRouting(
		serverIP.String(),
		gatewayIP.String(),
		tunInterface.Name,
		tunInterface.HardwareAddr.String(),
		netInterface.Name,
		tunGateway,
		tunDeviceIP,
		src,
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

	// Сигнал об успешной инициализации только после полного старта:
	// proxy поднят, wintun поднят, routing настроен.
	signalInit(initResult, nil)

	// Очистка при выходе
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
