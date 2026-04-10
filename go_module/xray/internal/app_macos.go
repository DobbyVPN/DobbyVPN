//go:build darwin && !(android || ios)

package internal

import (
	"context"
	"fmt"
	"sync"

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

	device, err := NewXrayDevice(*app.VlessConfig, app.RoutingConfig.RoutingTableID, "")
	if err != nil {
		err = fmt.Errorf("failed to create XrayDevice: %w", err)
		signalInit(initResult, err)
		return err
	}
	serverIP := device.GetServerIP()

	var closeOnce sync.Once
	closeAll := func() {
		closeOnce.Do(func() {
			tunnel.StopEngine()
			_ = device.Close()
		})
	}
	defer closeAll()

	go func() {
		<-ctx.Done()
		closeAll()
	}()

	defer func() {
		common.Client.MarkInCriticalSection(xrayCommon.Name)
		routing.StopRouting(serverIP.String(), gatewayIP.String())
		common.Client.MarkOutOffCriticalSection(xrayCommon.Name)
	}()

	if serverIP.String() != "127.0.0.1" {
		common.Client.MarkInCriticalSection(xrayCommon.Name)
		if err := routing.AddProxyRoute(serverIP.String(), gatewayIP.String()); err != nil {
			common.Client.MarkOutOffCriticalSection(xrayCommon.Name)
			err = fmt.Errorf("failed to add early route for server: %w", err)
			signalInit(initResult, err)
			return err
		}
		common.Client.MarkOutOffCriticalSection(xrayCommon.Name)
	}

	err = tunnel.StartEngine(platform_engine.EngineConfig{
		ProxyAddr:   device.GetProxyAddr(),
		FD:          -1,
		UplinkIface: "",
	})
	if err != nil {
		signalInit(initResult, err)
		return err
	}

	tunName := platform_engine.LastIface

	common.Client.MarkInCriticalSection(xrayCommon.Name)
	if err := routing.StartRouting(serverIP.String(), gatewayIP.String(), tunName); err != nil {
		common.Client.MarkOutOffCriticalSection(xrayCommon.Name)
		err = fmt.Errorf("failed to configure routing: %w", err)
		signalInit(initResult, err)
		return err
	}

	ifaceName, idx, err := protected_dialer.GetDefaultInterfaceNameDarwin(gatewayIP)
	if err != nil {
		log.Infof("[Xray][Darwin-Protect] failed to detect default interface: %v", err)
	} else {
		protected_dialer.SetDefaultInterface(idx)
		_ = routing.AddScopedDefaultRoute(ifaceName, gatewayIP.String())
		defer routing.DeleteScopedDefaultRoute(ifaceName)
	}

	common.Client.MarkOutOffCriticalSection(xrayCommon.Name)

	signalInit(initResult, nil)

	<-ctx.Done()
	return nil
}
