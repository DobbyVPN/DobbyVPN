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
		if err := routing.AddProxyRoute(serverIP.String(), gatewayIP.String()); err != nil {
			common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
			err = fmt.Errorf("failed to add early route for server: %w", err)
			signalInit(initResult, err)
			return err
		}
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
		log.Infof("[Routing] Early server route added successfully")
	} else {
		log.Infof("[Routing] Skipping early route for localhost (Cloak mode)")
	}

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
			tunnel.StopEngine()
			_ = ss.Close()
		})
	}

	defer closeAll()

	go func() {
		<-ctx.Done()
		closeAll()
		log.Infof("[Outline] Cancel received — closing interfaces")
	}()

	defer func() {
		common.Client.MarkInCriticalSection(outlineCommon.Name)
		log.Infof("[Routing] Cleaning up routes for %s...", serverIP.String())
		routing.StopRouting(serverIP.String(), gatewayIP.String())
		log.Infof("[Routing] Routes cleaned up")
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
	}()

	log.Infof("Outline/app: Start trafficCopyWg...\n")

	idx, err := tunnel.GetDefaultInterfaceIndexDarwin()
	if err == nil {
		tunnel.SetDefaultInterfaceIndex(idx)
	}

	log.Infof("[Tunnel] Starting tun2socks (darwin mode)...")

	tunName, err := tunnel.StartEngineDarwin(ss.GetProxyAddr())
	if err != nil {
		signalInit(initResult, err)
		return err
	}

	common.Client.MarkInCriticalSection(outlineCommon.Name)
	if err := routing.StartRouting(serverIP.String(), gatewayIP.String(), tunName); err != nil {
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
		err = fmt.Errorf("failed to configure routing: %w", err)
		signalInit(initResult, err)
		return err
	}
	common.Client.MarkOutOffCriticalSection(outlineCommon.Name)

	signalInit(initResult, nil)

	<-ctx.Done()

	log.Infof("[Tunnel] Context cancelled, stopping engine")
	return nil
}
