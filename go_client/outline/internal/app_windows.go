//go:build windows
// +build windows

package internal

import (
	"context"
	"errors"
	"fmt"
	"os"

	"go_client/common"
	"go_client/routing"
	"go_client/tunnel"

	"github.com/jackpal/gateway"
	"go_client/log"
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

func CreateEthernetPacket(dstMAC, srcMAC, ipPacket []byte) ([]byte, error) {
	if len(ipPacket) == 0 {
		return nil, errors.New("IP-packet is empty")
	}
	if len(dstMAC) != 6 || len(srcMAC) != 6 {
		return nil, errors.New("MAC addresses must be exactly 6 bytes long")
	}

	ethertype := []byte{0x08, 0x00} // Ethertype для IP

	ethernetPacket := append(dstMAC, srcMAC...)
	ethernetPacket = append(ethernetPacket, ethertype...)
	ethernetPacket = append(ethernetPacket, ipPacket...)

	return ethernetPacket, nil
}

func ExtractIPPacketFromEthernet(ethernetPacket []byte) ([]byte, error) {
	if len(ethernetPacket) < 14 {
		return nil, errors.New("packet is too short for Ethernet-title")
	}

	ethertype := (uint16(ethernetPacket[12]) << 8) | uint16(ethernetPacket[13])
	if ethertype != 0x0800 {
		return nil, errors.New("packet doesn't contain IP-data")
	}

	return ethernetPacket[14:], nil
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
	if !checkRoot() {
		err := fmt.Errorf("requires admin privileges")
		signalInit(initResult, err)
		return err
	}

	// 1. Определение шлюзов и IP (стандартная логика)
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		signalInit(initResult, err)
		return err
	}
	serverIP, err := ResolveServerIPFromConfig(*app.TransportConfig)
	if err != nil {
		signalInit(initResult, err)
		return err
	}

	// 2. Создание TUN/TAP устройства
	TunDeviceIP := "10.0.85.2"
	tun, err := newTunDevice(app.RoutingConfig.TunDeviceName, TunDeviceIP)
	if err != nil {
		signalInit(initResult, err)
		return err
	}
	defer tun.Close()

	// 3. Создание прокси-моста (Outline SOCKS5)
	ss, err := NewOutlineDevice(*app.TransportConfig)
	if err != nil {
		signalInit(initResult, err)
		return err
	}

	// 4. Настройка маршрутизации
	// Вызываем твой StartRouting как обычно
	tunInterface, _ := routing.GetNetworkInterfaceByIP(TunDeviceIP)
	netInterface, _ := routing.GetNetworkInterfaceByIP(gatewayIP.String())

	common.Client.MarkInCriticalSection(outlineCommon.Name)
	err = routing.StartRouting(
		serverIP.String(),
		gatewayIP.String(),
		tunInterface.Name,
		tunInterface.HardwareAddr.String(),
		netInterface.Name,
		"10.0.85.1",
		TunDeviceIP,
		nil, // src MAC можно передать если нужно
	)
	common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
	if err != nil {
		signalInit(initResult, err)
		return err
	}

	// 5. ЗАПУСК ДВИЖКА (Оптимальная часть)
	// Берем FD (Handle) из нашего устройства
	fd := tun.(*tunDevice).GetFd()

	log.Infof("[Windows] Starting tun2socks engine on Handle %d", fd)
	tunnel.StartEngine(fd, ss.GetProxyAddr())

	// Сигнал об успешном старте
	signalInit(initResult, nil)

	// Очистка при выходе
	defer func() {
		common.Client.MarkInCriticalSection(outlineCommon.Name)
		routing.StopRouting(serverIP.String(), tunInterface.Name, gatewayIP.String(), netInterface.Name, "10.0.85.1")
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
	}()

	// Ожидаем завершения контекста
	<-ctx.Done()

	log.Infof("[Tunnel] Stopping windows engine")
	tunnel.StopEngine()

	return nil
}
