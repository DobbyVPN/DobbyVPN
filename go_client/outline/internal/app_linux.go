//go:build linux
// +build linux

package internal

import (
	"context"
	"fmt"
	"github.com/jackpal/gateway"
	"go_client/common"
	"go_client/log"
	outlineCommon "go_client/outline/common"
	"go_client/routing"
	"go_client/tunnel"
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
	// 1. Подготовка сети
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		err = fmt.Errorf("failed to discover gateway: %w", err)
		signalInit(initResult, err)
		return err
	}

	serverIP, err := ResolveServerIPFromConfig(*app.TransportConfig)
	if err != nil {
		signalInit(initResult, err)
		return err
	}

	// 2. Создание TUN
	tun, err := newTunDevice(app.RoutingConfig.TunDeviceName, app.RoutingConfig.TunDeviceIP)
	if err != nil {
		signalInit(initResult, err)
		return err
	}
	// Мы закрываем его вручную при выходе
	defer tun.Close()

	// 3. Создание SOCKS5 прокси (Shadowsocks мост)
	ss, err := NewOutlineDevice(*app.TransportConfig)
	if err != nil {
		signalInit(initResult, err)
		return err
	}

	// 4. Настройка маршрутизации Linux (ip route)
	common.Client.MarkInCriticalSection(outlineCommon.Name)
	if err := routing.StartRouting(serverIP.String(), gatewayIP.String(), app.RoutingConfig.TunDeviceName); err != nil {
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
		signalInit(initResult, err)
		return err
	}
	common.Client.MarkOutOffCriticalSection(outlineCommon.Name)

	// 5. Запуск движка tun2socks
	// Твоя структура tunDevice теперь поддерживает GetFd()
	var fd int
	if t, ok := tun.(interface{ GetFd() int }); ok {
		fd = t.GetFd()
	} else {
		// Резервный вариант, если что-то пошло не так
		signalInit(initResult, fmt.Errorf("could not get file descriptor for TUN"))
		return nil
	}

	log.Infof("[Linux] Starting tun2socks engine on FD %d", fd)

	// Вызываем StartEngine (без присвоения ошибки, т.к. она void)
	tunnel.StartEngine(fd, ss.GetProxyAddr())

	// Успешная инициализация
	signalInit(initResult, nil)

	// Очистка маршрутов при остановке
	defer func() {
		common.Client.MarkInCriticalSection(outlineCommon.Name)
		log.Infof("[Routing] Cleaning up routes...")
		routing.StopRouting(serverIP.String(), gatewayIP.String())
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
	}()

	// 6. Ожидание завершения
	<-ctx.Done()

	log.Infof("[Tunnel] Stopping engine")
	tunnel.StopEngine()

	return nil
}
