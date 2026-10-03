package platform_engine

import (
	"context"
	"fmt"
	"net/url"

	"github.com/xjasonlyu/tun2socks/v2/core"
	"github.com/xjasonlyu/tun2socks/v2/core/device"
	"github.com/xjasonlyu/tun2socks/v2/proxy"
	"github.com/xjasonlyu/tun2socks/v2/tunnel"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

type EngineConfig struct {
	ProxyAddr   string
	FD          int    // Linux / Mobile
	UplinkIface string // Windows
}

func StartPlatformEngine(cfg EngineConfig) (bool, error) {
	return startPlatformEngine(cfg)
}

// EngineStop lets each platform preserve its required teardown order around
// tun2socks' device close and returns only after platform cleanup is complete.
func EngineStop(ctx context.Context) error {
	return stopPlatformEngine(ctx, stopStack)
}

func InterfaceName() string { return platformInterfaceName() }

// Access is serialized by tunnel.Engine, the existing process-wide owner.
// Only the public device, SOCKS, and stack APIs are used: no engine command
// hooks, global logger replacement, REST listener, or fatal-exit path.
var ownedDevice device.Device
var ownedStack *stack.Stack

func startStack(address string, open func() (device.Device, error)) (bool, error) {
	if ownedDevice != nil || ownedStack != nil {
		return false, fmt.Errorf("network stack is still owned")
	}
	endpoint, err := url.Parse("socks5://" + address)
	if err != nil {
		return false, fmt.Errorf("parse local SOCKS endpoint: %w", err)
	}
	password, _ := endpoint.User.Password()
	outbound, err := proxy.NewSocks5(endpoint.Host, endpoint.User.Username(), password)
	if err != nil {
		return false, fmt.Errorf("open local SOCKS proxy: %w", err)
	}
	dev, err := open()
	if err != nil {
		return false, err
	}
	ownedDevice = dev
	tunnel.T().SetDialer(outbound)
	ownedStack, err = core.CreateStack(&core.Config{LinkEndpoint: dev, TransportHandler: tunnel.T()})
	// From open onward the owner must call stop, including on a stack failure.
	return true, err
}

func stopStack() {
	if ownedDevice != nil {
		ownedDevice.Close()
		ownedDevice = nil
	}
	if ownedStack != nil {
		ownedStack.Close()
		ownedStack.Wait()
		ownedStack = nil
	}
}
