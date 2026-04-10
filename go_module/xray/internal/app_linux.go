//go:build linux && !(android || ios)

package internal

import (
	"context"
	"fmt"
	"sync"
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
	if app.VlessConfig == nil || *app.VlessConfig == "" {
		err := fmt.Errorf("vless config is required")
		signalInit(initResult, err)
		return err
	}
	if app.RoutingConfig == nil {
		err := fmt.Errorf("routing config is required")
		signalInit(initResult, err)
		return err
	}

	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		err = fmt.Errorf("failed to discover gateway: %w", err)
		signalInit(initResult, err)
		return err
	}

	// Detect uplink interface
	uplinkIface, err := routing.GetDefaultInterfaceNameLinux(gatewayIP.String())
	if err != nil {
		err = fmt.Errorf("failed to detect uplink interface: %w", err)
		signalInit(initResult, err)
		return err
	}

	// Device (Xray SOCKS bridge)
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
			log.Infof("[Xray][Linux][Lifecycle] Shutting down...")

			common.Client.MarkInCriticalSection(xrayCommon.Name)
			if err := routing.StopRouting(serverIP.String(), gatewayIP.String(), uplinkIface); err != nil {
				log.Infof("[Xray][Linux][StopRouting][ERROR] %v", err)
			}
			common.Client.MarkOutOffCriticalSection(xrayCommon.Name)

			if err := routing.CleanupMarkedRouting(
				app.RoutingConfig.RoutingTableID,
				app.RoutingConfig.RoutingTablePriority,
				uplinkIface,
				gatewayIP.String(),
			); err != nil {
				log.Infof("[Xray][Linux][CleanupMarkedRouting][WARN] %v", err)
			}

			tunnel.StopEngine()
			_ = device.Close()
		})
	}
	defer closeAll()

	// Early route
	if serverIP.String() != "127.0.0.1" {
		common.Client.MarkInCriticalSection(xrayCommon.Name)
		if err = routing.AddProxyRoute(serverIP.String(), gatewayIP.String(), uplinkIface); err != nil {
			common.Client.MarkOutOffCriticalSection(xrayCommon.Name)
			err = fmt.Errorf("failed to add early route: %w", err)
			signalInit(initResult, err)
			return err
		}
		common.Client.MarkOutOffCriticalSection(xrayCommon.Name)
	}

	// Marked routing + protected sockets
	common.Client.MarkInCriticalSection(xrayCommon.Name)
	if err = routing.SetupMarkedRouting(
		app.RoutingConfig.RoutingTableID,
		app.RoutingConfig.RoutingTablePriority,
		uplinkIface,
		gatewayIP.String(),
	); err != nil {
		common.Client.MarkOutOffCriticalSection(xrayCommon.Name)
		err = fmt.Errorf("failed to setup marked routing: %w", err)
		signalInit(initResult, err)
		return err
	}
	common.Client.MarkOutOffCriticalSection(xrayCommon.Name)

	protected_dialer.SetLinuxSocketMark(app.RoutingConfig.RoutingTableID)

	// Create tun
	tun, err := newTunDevice(app.RoutingConfig.TunDeviceName, app.RoutingConfig.TunDeviceIP)
	if err != nil {
		err = fmt.Errorf("failed to create TUN device: %w", err)
		signalInit(initResult, err)
		return err
	}
	defer func() { _ = tun.Close() }()

	t, ok := tun.(interface{ GetFd() int })
	if !ok {
		err = fmt.Errorf("TUN has no fd")
		signalInit(initResult, err)
		return err
	}
	fd := t.GetFd()
	if fd < 0 {
		err = fmt.Errorf("invalid fd=%d", fd)
		signalInit(initResult, err)
		return err
	}

	// tun2socks
	err = tunnel.StartEngine(platform_engine.EngineConfig{
		ProxyAddr:   device.GetProxyAddr(),
		FD:          fd,
		UplinkIface: "",
	})
	if err != nil {
		err = fmt.Errorf("failed to start tun2socks: %w", err)
		signalInit(initResult, err)
		return err
	}

	// prevent blackhole on route switch
	time.Sleep(300 * time.Millisecond)

	// routing switch
	common.Client.MarkInCriticalSection(xrayCommon.Name)
	if err = routing.StartRouting(serverIP.String(), gatewayIP.String(), uplinkIface, app.RoutingConfig.TunDeviceName); err != nil {
		common.Client.MarkOutOffCriticalSection(xrayCommon.Name)
		err = fmt.Errorf("failed to configure routing: %w", err)
		signalInit(initResult, err)
		return err
	}
	common.Client.MarkOutOffCriticalSection(xrayCommon.Name)

	signalInit(initResult, nil)

	<-ctx.Done()
	return nil
}
