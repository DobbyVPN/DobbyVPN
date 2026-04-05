//go:build linux && !(android || ios)
// +build linux,!android,!ios

package internal

import (
	"context"
	"fmt"
	"go_module/tunnel/platform_engine"
	"go_module/tunnel/protected_dialer"
	"sync"
	"time"

	"github.com/jackpal/gateway"

	"go_module/common"
	"go_module/log"
	outlineCommon "go_module/outline/common"
	"go_module/routing"
	"go_module/tunnel"
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
	log.Infof("[Linux][Init] ===== VPN initialization started =====")

	// 1. discover gateway
	log.Infof("[Linux][Step 1] Discovering default gateway...")
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		err = fmt.Errorf("failed to discover gateway: %w", err)
		log.Infof("[Linux][Step 1][ERROR] %v", err)
		signalInit(initResult, err)
		return err
	}
	log.Infof("[Linux][Step 1][OK] Gateway=%s", gatewayIP.String())

	// 2. resolve VPN server IP
	log.Infof("[Linux][Step 2] Resolving VPN server IP...")
	serverIP, err := ResolveServerIPFromConfig(*app.TransportConfig)
	if err != nil {
		err = fmt.Errorf("failed to resolve server IP from config: %w", err)
		log.Infof("[Linux][Step 2][ERROR] %v", err)
		signalInit(initResult, err)
		return err
	}
	log.Infof("[Linux][Step 2][OK] ServerIP=%s", serverIP.String())

	// 3. detect physical default interface
	log.Infof("[Linux][Step 3] Detecting uplink interface...")
	uplinkIface, err := routing.GetDefaultInterfaceNameLinux(gatewayIP.String())
	if err != nil {
		err = fmt.Errorf("failed to detect uplink interface: %w", err)
		log.Infof("[Linux][Step 3][ERROR] %v", err)
		signalInit(initResult, err)
		return err
	}
	log.Infof("[Linux][Step 3][OK] Uplink interface=%s", uplinkIface)

	// 4. early route
	if serverIP.String() != "127.0.0.1" {
		log.Infof("[Linux][Step 4] Installing early route → %s via %s dev %s",
			serverIP, gatewayIP, uplinkIface)

		common.Client.MarkInCriticalSection(outlineCommon.Name)
		if err = routing.AddProxyRoute(serverIP.String(), gatewayIP.String(), uplinkIface); err != nil {
			common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
			err = fmt.Errorf("failed to add early route: %w", err)
			log.Infof("[Linux][Step 4][ERROR] %v", err)
			signalInit(initResult, err)
			return err
		}
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)

		log.Infof("[Linux][Step 4][OK] Early route installed")
	} else {
		log.Infof("[Linux][Step 4] Skipped (localhost / Cloak)")
	}

	// 5. marked routing
	log.Infof("[Linux][Step 5] Setting up policy routing (fwmark=%d table=%d priority=%d)",
		app.RoutingConfig.RoutingTableID,
		app.RoutingConfig.RoutingTableID,
		app.RoutingConfig.RoutingTablePriority,
	)

	common.Client.MarkInCriticalSection(outlineCommon.Name)
	if err = routing.SetupMarkedRouting(
		app.RoutingConfig.RoutingTableID,
		app.RoutingConfig.RoutingTablePriority,
		uplinkIface,
		gatewayIP.String(),
	); err != nil {
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
		err = fmt.Errorf("failed to setup marked routing: %w", err)
		log.Infof("[Linux][Step 5][ERROR] %v", err)
		signalInit(initResult, err)
		return err
	}
	common.Client.MarkOutOffCriticalSection(outlineCommon.Name)

	log.Infof("[Linux][Step 5][OK] Policy routing configured")

	// protected sockets
	protected_dialer.SetLinuxSocketMark(app.RoutingConfig.RoutingTableID)

	log.Infof("[Linux][Step 5] Protected dialers installed (SO_MARK=%d)", app.RoutingConfig.RoutingTableID)

	// 6. create TUN
	log.Infof("[Linux][Step 6] Creating TUN: name=%s ip=%s",
		app.RoutingConfig.TunDeviceName,
		app.RoutingConfig.TunDeviceIP,
	)

	tun, err := newTunDevice(app.RoutingConfig.TunDeviceName, app.RoutingConfig.TunDeviceIP)
	if err != nil {
		err = fmt.Errorf("failed to create TUN device: %w", err)
		log.Infof("[Linux][Step 6][ERROR] %v", err)
		signalInit(initResult, err)
		return err
	}

	log.Infof("[Linux][Step 6][OK] TUN created: %s", app.RoutingConfig.TunDeviceName)

	// 7. Outline
	log.Infof("[Linux][Step 7] Creating Outline SOCKS bridge...")
	ss, err := NewOutlineDevice(*app.TransportConfig)
	if err != nil {
		_ = tun.Close()
		err = fmt.Errorf("failed to create OutlineDevice: %w", err)
		log.Infof("[Linux][Step 7][ERROR] %v", err)
		signalInit(initResult, err)
		return err
	}
	log.Infof("[Linux][Step 7][OK] SOCKS5 proxy=%s", ss.GetProxyAddr())

	var closeOnce sync.Once
	closeAll := func() {
		closeOnce.Do(func() {
			log.Infof("[Linux][Lifecycle] Shutting down...")

			common.Client.MarkInCriticalSection(outlineCommon.Name)
			if err = routing.StopRouting(serverIP.String(), gatewayIP.String(), uplinkIface); err != nil {
				log.Infof("[Linux][StopRouting][ERROR] %v", err)
			}
			common.Client.MarkOutOffCriticalSection(outlineCommon.Name)

			if err = routing.CleanupMarkedRouting(
				app.RoutingConfig.RoutingTableID,
				app.RoutingConfig.RoutingTablePriority,
				uplinkIface,
				gatewayIP.String(),
			); err != nil {
				log.Infof("[Linux][CleanupMarkedRouting][WARN] %v", err)
			}

			tunnel.StopEngine()

			_ = ss.Close()

			_ = tun.Close()

			log.Infof("[Linux][Lifecycle] Shutdown complete")
		})
	}

	defer closeAll()

	// 8. fd
	t, ok := tun.(interface{ GetFd() int })
	if !ok {
		err = fmt.Errorf("TUN has no fd")
		log.Infof("[Linux][Step 8][ERROR] %v", err)
		signalInit(initResult, err)
		return err
	}
	fd := t.GetFd()
	if fd < 0 {
		err = fmt.Errorf("invalid fd=%d", fd)
		log.Infof("[Linux][Step 8][ERROR] %v", err)
		signalInit(initResult, err)
		return err
	}
	log.Infof("[Linux][Step 8][OK] fd=%d", fd)

	// 9. tun2socks
	log.Infof("[Linux][Step 9] Starting tun2socks (fd=%d proxy=%s)", fd, ss.GetProxyAddr())
	err = tunnel.StartEngine(platform_engine.EngineConfig{
		ProxyAddr:   ss.GetProxyAddr(),
		FD:          fd,
		UplinkIface: "",
	})
	if err != nil {
		log.Infof("Can't start tun2socks: %v", err)
		return err
	}

	log.Infof("[Linux][Step 9][OK] tun2socks started — waiting for readiness...")

	// FIX: предотвращаем blackhole
	time.Sleep(300 * time.Millisecond)

	// 10. routing switch
	log.Infof("[Linux][Step 10] Switching default route → TUN (%s)", app.RoutingConfig.TunDeviceName)

	common.Client.MarkInCriticalSection(outlineCommon.Name)
	if err = routing.StartRouting(serverIP.String(), gatewayIP.String(), uplinkIface, app.RoutingConfig.TunDeviceName); err != nil {
		common.Client.MarkOutOffCriticalSection(outlineCommon.Name)
		err = fmt.Errorf("failed to configure routing: %w", err)
		log.Infof("[Linux][Step 10][ERROR] %v", err)
		signalInit(initResult, err)
		return err
	}
	common.Client.MarkOutOffCriticalSection(outlineCommon.Name)

	log.Infof("[Linux][Step 10][OK] Default route switched to VPN")

	log.Infof("[Linux][Init] ===== VPN started successfully =====")

	signalInit(initResult, nil)

	<-ctx.Done()

	log.Infof("[Tunnel] Stopping engine")
	tunnel.StopEngine()

	log.Infof("[Linux][Lifecycle] Context cancelled — stopping engine")
	return nil
}
