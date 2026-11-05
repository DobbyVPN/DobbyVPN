package platform_engine

import (
	"bytes"
	"context"
	"core/log"
	"fmt"
	"net/url"
	"runtime"
	"time"

	"github.com/xjasonlyu/tun2socks/v2/core"
	stackAdapter "github.com/xjasonlyu/tun2socks/v2/core/adapter"
	"github.com/xjasonlyu/tun2socks/v2/core/device"
	"github.com/xjasonlyu/tun2socks/v2/proxy"
	"github.com/xjasonlyu/tun2socks/v2/tunnel"
	"github.com/xjasonlyu/tun2socks/v2/tunnel/statistic"
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

func stopPhase(name string, fn func()) {
	started := time.Now()
	log.Debugf(Category, "[Engine][Platform] phase=%s started", name)
	fn()
	log.Debugf(Category, "[Engine][Platform] phase=%s finished elapsed=%s", name, time.Since(started))
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

// goroutineDumpLimit bounds the timeout snapshot so a wedged teardown cannot
// flood the rotating diagnostic log while the process is already unhealthy.
const goroutineDumpLimit = 16 << 10

// liveCounters reports only gauges reachable from this package without new
// dependency state: tun2socks' process-wide flow trackers and gVisor's TCP
// gauges on the owned stack. Engine.trackedConn lives in package tunnel and is
// not importable here (tunnel imports this package), so its counter is omitted
// rather than adding product API for a DEBUG line.
func liveCounters() string {
	conns := len(statistic.DefaultManager.Snapshot().Connections)
	var established, connected uint64
	if ownedStack != nil {
		stats := ownedStack.Stats().TCP
		if stats.CurrentEstablished != nil {
			established = stats.CurrentEstablished.Value()
		}
		if stats.CurrentConnected != nil {
			connected = stats.CurrentConnected.Value()
		}
	}
	return fmt.Sprintf("tun2socks_conns=%d gvisor_tcp_established=%d gvisor_tcp_connected=%d", conns, established, connected)
}

// logTimeoutDump captures every goroutine once the cleanup deadline expires.
// deadline-expires dump for #272 family diagnosis; DEBUG-only. It runs off the
// stop path so a slow snapshot cannot delay publishing the cleanup failure.
func logTimeoutDump(name string) {
	buf := make([]byte, goroutineDumpLimit+1)
	n := runtime.Stack(buf, true)
	truncated := n > goroutineDumpLimit
	if truncated {
		n = goroutineDumpLimit
	}
	buf = buf[:n]
	if truncated {
		if cut := bytes.LastIndexByte(buf, '\n'); cut >= 0 {
			buf = buf[:cut]
		}
	}
	log.Debugf(Category, "[Engine][Platform] phase=%s timed_out goroutine dump:", name)
	for len(buf) > 0 {
		line := buf
		if end := bytes.IndexByte(buf, '\n'); end >= 0 {
			line, buf = buf[:end], buf[end+1:]
		} else {
			buf = nil
		}
		log.Debugf(Category, "[Engine][Platform] %s", line)
	}
	if truncated {
		log.Debugf(Category, "[Engine][Platform] phase=%s timed_out dump (truncated)", name)
	}
	log.Debugf(Category, "[Engine][Platform] phase=%s timed_out dump_bytes=%d done=true", name, n)
}

func stopPhaseCtx(ctx context.Context, name string, fn func()) error {
	run := stopPhases[name]
	log.Debugf(Category, "[Engine][Platform] phase=%s point=before %s", name, liveCounters())
	if run == nil {
		run = &phaseRun{started: time.Now(), done: make(chan struct{})}
		stopPhases[name] = run
		go func() {
			defer close(run.done)
			stopPhase(name, fn)
		}()
	}
	select {
	case <-run.done:
		log.Debugf(Category, "[Engine][Platform] phase=%s point=after %s", name, liveCounters())
		return nil
	case <-ctx.Done():
		err := ctx.Err()
		log.Debugf(Category, "[Engine][Platform] phase=%s timed_out=true elapsed=%s err=%v", name, time.Since(run.started), err)
		go logTimeoutDump(name)
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
