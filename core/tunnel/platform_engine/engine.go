package platform_engine

import (
	"context"
	"core/log"
	"fmt"
	"net/url"
	"time"

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
	if dev != nil {
		ownedDevice = dev
	}
	if err != nil {
		return dev != nil, err
	}
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

// testStackWaitHook is inert in every product build. A build-local
// qualification binary installs a hold here to prove the hang capture against a
// genuinely blocked stack-wait stage.
var testStackWaitHook func()

func stopPhase(name string, fn func()) {
	ended := log.StageBegin("cleanup", name, "")
	started := time.Now()
	log.Debugf(Category, "[Engine][Platform] phase=%s started", name)
	if name == "stack-wait" && testStackWaitHook != nil {
		testStackWaitHook()
	}
	fn()
	log.Debugf(Category, "[Engine][Platform] phase=%s finished elapsed=%s", name, time.Since(started))
	ended("ok")
}

type phaseRun struct {
	started time.Time
	done    chan struct{}
}

// stopPhases memoizes teardown phases so a phase that outlived its deadline is
// awaited again by the next stop instead of being invoked twice. Entries are
// cleared once the whole stack teardown succeeds. Accessed only from the
// serialized stop path; the background goroutine only closes done.
var stopPhases = map[string]*phaseRun{}

func stopPhaseCtx(ctx context.Context, name string, fn func()) error {
	run := stopPhases[name]
	if run == nil {
		run = &phaseRun{started: time.Now(), done: make(chan struct{})}
		stopPhases[name] = run
		go func() {
			defer close(run.done)
			stopPhase(name, fn)
		}()
	}
	// A completed cleanup remains successful even if its caller's deadline
	// expired before the retry observed it.
	select {
	case <-run.done:
		return nil
	default:
	}
	select {
	case <-run.done:
		return nil
	case <-ctx.Done():
		err := ctx.Err()
		log.Warnf(Category, "[Engine][Platform] phase=%s timed_out=true elapsed=%s err=%v", name, time.Since(run.started), err)
		log.StageMark("cleanup", name, "", "timeout")
		return fmt.Errorf("platform stop phase %s exceeded cleanup deadline: %w", name, err)
	}
}

func drainStack(ctx context.Context) error {
	if err := stopPhaseCtx(ctx, "stack-close", ownedStack.Close); err != nil {
		return err
	}
	if err := stopPhaseCtx(ctx, "stack-wait", ownedStack.Wait); err != nil {
		return err
	}
	ownedStack = nil
	return nil
}

func stopStack(ctx context.Context) error {
	if ownedDevice != nil {
		if err := stopPhaseCtx(ctx, "device-close", ownedDevice.Close); err != nil {
			return err
		}
		ownedDevice = nil
	}
	if ownedStack != nil {
		if err := drainStack(ctx); err != nil {
			return err
		}
	}
	clear(stopPhases)
	return nil
}
