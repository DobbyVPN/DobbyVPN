package platform_engine

import (
	"context"
	"core/log"
	"fmt"
	"net/url"

	"github.com/xjasonlyu/tun2socks/v2/core"
	stackAdapter "github.com/xjasonlyu/tun2socks/v2/core/adapter"
	"github.com/xjasonlyu/tun2socks/v2/core/device"
	"github.com/xjasonlyu/tun2socks/v2/proxy"
	"github.com/xjasonlyu/tun2socks/v2/tunnel"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

// TunnelMTU meets IPv6's minimum while leaving protocol overhead to the transport.
const TunnelMTU = 1280

type EngineConfig struct {
	Dialer           proxy.Proxy
	ProxyAddr        string
	FD               int    // Linux / Mobile
	UplinkIface      string // Windows
	TransportHandler stackAdapter.TransportHandler
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

func startStack(cfg EngineConfig, open func() (device.Device, error)) (bool, error) {
	if ownedDevice != nil || ownedStack != nil {
		return false, fmt.Errorf("network stack is still owned")
	}
	outbound := cfg.Dialer
	if outbound == nil {
		endpoint, err := url.Parse("socks5://" + cfg.ProxyAddr)
		if err != nil {
			return false, fmt.Errorf("parse local SOCKS endpoint: %w", err)
		}
		password, _ := endpoint.User.Password()
		outbound, err = proxy.NewSocks5(endpoint.Host, endpoint.User.Username(), password)
		if err != nil {
			return false, fmt.Errorf("open local SOCKS proxy: %w", err)
		}
	}
	dev, err := open()
	if err != nil {
		return false, err
	}
	ownedDevice = dev
	if dev.MTU() < TunnelMTU {
		return true, fmt.Errorf("tunnel MTU %d is below IPv6 minimum %d", dev.MTU(), TunnelMTU)
	}
	tunnel.T().SetDialer(outbound)
	handler := cfg.TransportHandler
	if handler == nil {
		handler = tunnel.T()
	}
	ownedStack, err = core.CreateStack(&core.Config{LinkEndpoint: dev, TransportHandler: handler})
	if err == nil {
		log.Infof(Category, "packet stack ready interface=%s mtu=%d ipv4=true ipv6=true", dev.Name(), dev.MTU())
	}
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
