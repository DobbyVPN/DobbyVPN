//go:build darwin
// +build darwin

package internal

import (
	"context"
	"fmt"
	"go_client/log"
	"sync"
	//"time"

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
	// this WaitGroup must Wait() after tun is closed

	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		signalInit(initResult, err)
		return err
	}

	log.Infof("gatewayIP: %s", gatewayIP.String())

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
		routing.AddProxyRoute(serverIP.String(), gatewayIP.String())
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
		log.Infof("[Routing] Early server route added successfully")
	} else {
		log.Infof("[Routing] Skipping early route for localhost (Cloak mode)")
	}

	tun, err := newTunDevice(app.RoutingConfig.TunDeviceName, app.RoutingConfig.TunDeviceIP)
	if err != nil {
		err = fmt.Errorf("failed to create tun device: %w, open app with sudo", err)
		signalInit(initResult, err)
		return err
	}
	defer tun.Close()

	log.Infof("Tun created")

	ss, err := NewOutlineDevice(*app.TransportConfig)
	if err != nil {
		err = fmt.Errorf("failed to create OutlineDevice: %w", err)
		signalInit(initResult, err)
		return err
	}

	log.Infof("Device created")

	var closeOnce sync.Once
	closeAll := func() {
		closeOnce.Do(func() {
			log.Infof("[Outline] Closing interfaces")
			_ = tun.Close()
		})
	}

	defer closeAll()

	go func() {
		<-ctx.Done()
		closeAll()
		log.Infof("[Outline] Cancel received — closing interfaces")
	}()

	common.Client.MarkInCriticalSection(outlineCommon.Name)
	if err := routing.StartRouting(serverIP.String(), gatewayIP.String(), tun.(*tunDevice).name); err != nil {
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
		err = fmt.Errorf("failed to configure routing: %w", err)
		signalInit(initResult, err)
		return err
	}
	common.Client.MarkOutOffCriticalSection(outlineCommon.Name)

	// Signal successful initialization
	signalInit(initResult, nil)

	defer func() {
		common.Client.MarkInCriticalSection(outlineCommon.Name)
		log.Infof("[Routing] Cleaning up routes for %s...", serverIP.String())
		routing.StopRouting(serverIP.String(), gatewayIP.String())
		log.Infof("[Routing] Routes cleaned up")
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
	}()

	log.Infof("Outline/app: Start trafficCopyWg...\n")

	var fd int
	if t, ok := tun.(interface{ GetFd() int }); ok {
		fd = t.GetFd()
	} else {
		// Резервный вариант, если что-то пошло не так
		signalInit(initResult, fmt.Errorf("could not get file descriptor for TUN"))
		return nil
	}

	log.Infof("[Tunnel] Starting tun2socks engine...")
	tunnel.StartEngine(fd, ss.GetProxyAddr())

	// Сигнализируем об успешном запуске
	signalInit(initResult, nil)

	<-ctx.Done()

	log.Infof("[Tunnel] Context cancelled, stopping engine")
	tunnel.StopEngine() // Вместо StopTransfer
	return nil
}
