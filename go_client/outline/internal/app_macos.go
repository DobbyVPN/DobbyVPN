//go:build darwin
// +build darwin

package internal

import (
	"errors"
	"fmt"
	log "go_client/logger"
	//"os/exec"
	"context"
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

	if !checkRoot() {
		err := errors.New("this operation requires superuser privileges. Please run the program with sudo or as root")
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
	defer ss.Close()

	if err := ss.Refresh(); err != nil {
		err = fmt.Errorf("failed to refresh OutlineDevice: %w", err)
		signalInit(initResult, err)
		return err
	}

	log.Infof("Device created")

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
		func(p []byte) (int, error) {
			return ss.Read(p)
		},
		func(b []byte) (int, error) {
			return ss.Write(b)
		},
	)

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

	<-ctx.Done()

	log.Infof("[Tunnel] Context cancelled — stopping transfer")
	tunnel.StopTransfer()

	log.Infof("Outline/app: received interrupt signal, terminating...\n")

	tun.Close()
	ss.Close()

	return nil
}
