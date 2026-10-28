package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	"github.com/xjasonlyu/tun2socks/v2/proxy"
	"github.com/xjasonlyu/tun2socks/v2/proxy/proto"
	stackTunnel "github.com/xjasonlyu/tun2socks/v2/tunnel"
	"github.com/xjasonlyu/tun2socks/v2/tunnel/statistic"

	"core/dnscache"
	"core/log"
	"core/tunnel/platform_engine"
	"core/tunnel/protected_dialer"
)

var (
	engineMu     sync.Mutex
	activeEngine *Engine
)

// ErrEngineBusy means another session currently owns tun2socks.  tun2socks
// itself is process-global, so replacing an owner here would disconnect an
// unrelated VPN session.
var ErrEngineBusy = fmt.Errorf("tun2socks engine is busy")

// Engine is the ownership handle for one tun2socks session.  Stop only tears
// down the engine if this handle still owns it, and is safe to call repeatedly.
//
// The underlying tun2socks package is process-global.  This handle makes that
// limitation explicit at the Dobby boundary instead of allowing a later start
// or stop request to affect a different lifecycle generation.
type Engine struct {
	mu        sync.RWMutex
	stopped   bool
	stopErr   error
	ifaceName string
	proxy     *DobbyProxy
	router    *switchingProxy
	attempt   uint64

	// stopPlatform exists so ownership bookkeeping can be tested without a
	// real TUN device. Production handles use platform_engine.EngineStop.
	stopPlatform func(context.Context) error
}

const udpAssociationIdleTimeout = 10 * time.Second

type DobbyProxy struct {
	vpn        proxy.Proxy
	direct     proxy.Proxy
	bypass     *BypassPolicy
	flows      flowOwner
	generation uint64
	udpRelay   *stackTunnel.Tunnel

	activeTCP atomic.Int64
	activeUDP atomic.Int64
	peakTCP   atomic.Int64
	peakUDP   atomic.Int64

	tcpDialAttempt atomic.Uint64
	udpDialAttempt atomic.Uint64
	udpIdleTimeout atomic.Uint64
}

type trackedConn struct {
	net.Conn
	route    string
	dest     string
	started  time.Time
	release  func() int64
	mu       sync.Mutex
	closed   bool
	owner    *flowOwner
	closeErr error
}

func (c *trackedConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closeErr = c.Conn.Close()
	if c.closeErr != nil && !errors.Is(c.closeErr, net.ErrClosed) {
		if c.owner != nil {
			c.owner.release(c, c.closeErr)
		}
		log.Errorf(Category, "[Router] TCP close failed route=%s dest=%s lifetime=%s err=%v", c.route, c.dest, time.Since(c.started), c.closeErr)
		return c.closeErr
	}
	c.closed, c.closeErr = true, nil
	active := c.release()
	if c.owner != nil {
		c.owner.release(c, nil)
	}
	log.Debugf(Category, "[Router] TCP closed route=%s dest=%s lifetime=%s activeTCP=%d", c.route, c.dest, time.Since(c.started), active)
	return nil
}

func (c *trackedConn) CloseRead() error {
	if conn, ok := c.Conn.(interface{ CloseRead() error }); ok {
		return conn.CloseRead()
	}
	return errors.ErrUnsupported
}

func (c *trackedConn) CloseWrite() error {
	if conn, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return conn.CloseWrite()
	}
	return errors.ErrUnsupported
}

type trackedPacketConn struct {
	net.PacketConn
	route    string
	dest     string
	started  time.Time
	release  func() int64
	mu       sync.Mutex
	closed   bool
	closeErr error
}

func (c *trackedPacketConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closeErr = c.PacketConn.Close()
	if c.closeErr != nil && !errors.Is(c.closeErr, net.ErrClosed) {
		log.Errorf(Category, "[Router] UDP close failed route=%s dest=%s lifetime=%s err=%v", c.route, c.dest, time.Since(c.started), c.closeErr)
		return c.closeErr
	}
	c.closed, c.closeErr = true, nil
	active := c.release()
	log.Debugf(Category, "[Router] UDP closed route=%s dest=%s lifetime=%s activeUDP=%d", c.route, c.dest, time.Since(c.started), active)
	return nil
}

type idlePacketConn struct {
	net.PacketConn
	timeout       time.Duration
	timer         *time.Timer
	route         string
	dest          string
	onIdleTimeout func() uint64
	mu            sync.Mutex
	lastTouch     time.Time
	closed        bool
	closeErr      error
	owner         *flowOwner
}

func newIdlePacketConn(conn net.PacketConn, timeout time.Duration, route, dest string, onIdleTimeout func() uint64, owner *flowOwner) *idlePacketConn {
	c := &idlePacketConn{
		PacketConn:    conn,
		timeout:       timeout,
		route:         route,
		dest:          dest,
		onIdleTimeout: onIdleTimeout,
		owner:         owner,
	}
	c.timer = time.AfterFunc(timeout, c.closeAfterIdleTimeout)
	return c
}

func (c *idlePacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, addr, err := c.PacketConn.ReadFrom(b)
	if n > 0 {
		c.touch()
	}
	return n, addr, err
}

func (c *idlePacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	n, err := c.PacketConn.WriteTo(b, addr)
	if n > 0 {
		c.touch()
	}
	return n, err
}

func (c *idlePacketConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed && c.closeErr == nil {
		return nil
	}
	c.closed = true
	c.timer.Stop()
	c.closeErr = c.PacketConn.Close()
	if c.owner != nil {
		c.owner.release(c, c.closeErr)
	}
	return c.closeErr
}

func (c *idlePacketConn) closeAfterIdleTimeout() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true

	var count uint64
	if c.onIdleTimeout != nil {
		count = c.onIdleTimeout()
	}
	log.Debugf(Category, "[Router] UDP idle timeout route=%s dest=%s timeout=%s count=%d", c.route, c.dest, c.timeout, count)
	c.closeErr = c.PacketConn.Close()
	if c.owner != nil {
		c.owner.release(c, c.closeErr)
	}
}

func (c *idlePacketConn) touch() {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed && now.Sub(c.lastTouch) > time.Second {
		c.lastTouch = now
		c.timer.Reset(c.timeout)
	}
}

func (p *DobbyProxy) DialContext(ctx context.Context, metadata *M.Metadata) (net.Conn, error) {
	start := time.Now()
	dest := metadata.DestinationAddress()
	attempt := p.tcpDialAttempt.Add(1)

	route, px := "VPN", p.vpn
	if p.bypass.IsBypass(metadata) {
		route, px = "DIRECT", p.direct
	}
	log.Debugf(Category, "[Router] TCP dial attempt=%d route=%s dstIP=%s dest=%s proto=%s stats={%s}", attempt, route, metadata.DstIP, dest, metadata.Network, p.flowStats())
	return p.dialTCPRoute(ctx, metadata, route, px, attempt, dest, start)
}

func (p *DobbyProxy) dialTCPRoute(ctx context.Context, metadata *M.Metadata, route string, px proxy.Proxy, attempt uint64, dest string, start time.Time) (net.Conn, error) {
	if err := p.flows.beginDial(); err != nil {
		return nil, err
	}
	defer p.flows.endDial()
	ctx, finish := p.flows.pendingTCPContext(ctx)
	defer finish()
	active := p.activeTCP.Add(1)
	release := func() int64 { return p.activeTCP.Add(-1) }
	updatePeakInt64(&p.peakTCP, active)
	conn, err := px.DialContext(ctx, metadata)
	if err != nil {
		release()
		log.Debugf(Category, "[Router] %s TCP dial error attempt=%d dest=%s elapsed=%s stats={%s} err=%v", route, attempt, dest, time.Since(start), p.flowStats(), err)
		return nil, err
	}
	log.Debugf(Category, "[Router] %s TCP dial OK attempt=%d dest=%s elapsed=%s local=%s remote=%s stats={%s}", route, attempt, dest, time.Since(start), conn.LocalAddr(), conn.RemoteAddr(), p.flowStats())
	tracked := &trackedConn{Conn: conn, release: release, route: route, dest: dest, started: time.Now(), owner: &p.flows}
	if !p.flows.track(tracked) {
		closeErr := tracked.Close()
		log.Debugf(Category, "[Router] late TCP dial closed route=%s dest=%s elapsed=%s err=%v", route, dest, time.Since(start), closeErr)
		return nil, errors.Join(net.ErrClosed, closeErr)
	}
	return tracked, nil
}

func (p *DobbyProxy) DialUDP(metadata *M.Metadata) (net.PacketConn, error) {
	start := time.Now()
	dest := metadata.DestinationAddress()
	attempt := p.udpDialAttempt.Add(1)

	route, px := "VPN", p.vpn
	if p.bypass.IsBypass(metadata) {
		route, px = "DIRECT", p.direct
	}
	log.Debugf(Category, "[Router] UDP dial attempt=%d route=%s dstIP=%s dest=%s proto=%s stats={%s}", attempt, route, metadata.DstIP, dest, metadata.Network, p.flowStats())
	return p.dialUDPRoute(metadata, route, px, attempt, dest, start)
}

func (p *DobbyProxy) dialUDPRoute(metadata *M.Metadata, route string, px proxy.Proxy, attempt uint64, dest string, start time.Time) (net.PacketConn, error) {
	if err := p.flows.beginDial(); err != nil {
		return nil, err
	}
	defer p.flows.endDial()
	active := p.activeUDP.Add(1)
	release := func() int64 { return p.activeUDP.Add(-1) }
	updatePeakInt64(&p.peakUDP, active)
	conn, err := px.DialUDP(metadata)
	if err != nil {
		release()
		log.Debugf(Category, "[Router] %s UDP dial error attempt=%d dest=%s elapsed=%s stats={%s} err=%v", route, attempt, dest, time.Since(start), p.flowStats(), err)
		return nil, err
	}
	log.Debugf(Category, "[Router] %s UDP dial OK attempt=%d dest=%s elapsed=%s local=%s stats={%s}", route, attempt, dest, time.Since(start), conn.LocalAddr(), p.flowStats())
	tracked := &trackedPacketConn{PacketConn: conn, release: release, route: route, dest: dest, started: time.Now()}
	owned := newIdlePacketConn(tracked, udpAssociationIdleTimeout, route, dest, func() uint64 {
		return p.udpIdleTimeout.Add(1)
	}, &p.flows)
	owned.mu.Lock()
	accepted := !owned.closed && p.flows.track(owned)
	owned.mu.Unlock()
	if !accepted {
		closeErr := owned.Close()
		log.Debugf(Category, "[Router] late UDP dial closed route=%s dest=%s elapsed=%s err=%v", route, dest, time.Since(start), closeErr)
		return nil, errors.Join(net.ErrClosed, closeErr)
	}
	return owned, nil
}

func (p *DobbyProxy) Addr() string {
	return p.vpn.Addr()
}

func (p *DobbyProxy) Proto() proto.Proto {
	return p.vpn.Proto()
}

// StartOwnedEngine starts tun2socks and returns the handle which exclusively
// owns the resulting process-global engine. A second start is rejected rather
// than stopping or reconfiguring the current session.
func StartOwnedEngine(cfg platform_engine.EngineConfig, dnsCache *dnscache.Cache, bypass *BypassPolicy) (*Engine, error) {
	engineMu.Lock()
	defer engineMu.Unlock()

	if activeEngine != nil {
		return nil, ErrEngineBusy
	}
	handle, _, err := startOwnedEngineLocked(cfg, dnsCache, bypass)
	return handle, err
}

// startOwnedEngineLocked reports whether the platform engine accepted its
// configuration. Once accepted, that platform engine exclusively owns every
// resource embedded in cfg, including an fd-backed device, and its stop path
// releases those resources on both later startup failure and normal shutdown.
// Before acceptance, ownership remains with the caller.
func startOwnedEngineLocked(cfg platform_engine.EngineConfig, dnsCache *dnscache.Cache, bypass *BypassPolicy) (*Engine, bool, error) {
	if dnsCache == nil {
		return nil, false, errors.New("DNS cache is required for tunnel start")
	}
	if bypass == nil {
		return nil, false, errors.New("bypass policy is required for tunnel start")
	}

	handle := &Engine{stopPlatform: platform_engine.EngineStop, router: newSwitchingProxy()}
	activeEngine = handle
	// Install the stable router before the descriptor can receive packets.
	// No transport exists until an attempt has passed its readiness check.
	cfg.Dialer = handle.router
	cfg.TransportHandler = handle.router
	if cfg.ProxyAddr != "" {
		if err := handle.attachLocked(context.Background(), 0, cfg.ProxyAddr, dnsCache, bypass); err != nil {
			activeEngine = nil
			return nil, false, err
		}
	}
	if accepted, err := platform_engine.StartPlatformEngine(cfg); err != nil {
		if accepted {
			return handle, true, err
		}
		activeEngine = nil
		return nil, false, errors.Join(err, handle.detachLocked(context.Background()))
	}
	handle.ifaceName = platform_engine.InterfaceName()
	log.Infof(Category, "[Engine] network owner active interface=%s", handle.ifaceName)
	return handle, true, nil
}

// Attach admits packets only to a verified protocol attempt. A prior failed
// detach retains its flows and prevents another target from being installed.
func (e *Engine) Attach(ctx context.Context, generation uint64, address string, cache *dnscache.Cache, bypass *BypassPolicy) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stopped || e.router == nil {
		return net.ErrClosed
	}
	if e.proxy != nil {
		return errors.New("previous attempt still owns flows")
	}
	return e.attachLocked(ctx, generation, address, cache, bypass)
}

func (e *Engine) attachLocked(ctx context.Context, generation uint64, address string, cache *dnscache.Cache, bypass *BypassPolicy) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cache == nil || bypass == nil {
		return errors.New("attempt resolver and bypass policy are required")
	}
	endpoint, err := url.Parse("socks5://" + address)
	if err != nil {
		return err
	}
	password, _ := endpoint.User.Password()
	outbound, err := proxy.NewSocks5(endpoint.Host, endpoint.User.Username(), password)
	if err != nil {
		return err
	}
	wrapper := &DobbyProxy{vpn: outbound, direct: &protected_dialer.ProtectedDirectProxy{Proxy: proxy.NewDirect(), DNSCache: cache, OwnerContext: ctx}, bypass: bypass, generation: generation}
	wrapper.vpn, err = newSocksTCPProxy(outbound, address, &wrapper.flows)
	if err != nil {
		return err
	}
	// Bind the UDP dispatcher to this attempt. An admitted flow keeps its
	// target even if dispatch runs after replacement of the active attempt.
	wrapper.udpRelay = stackTunnel.New(wrapper, statistic.DefaultManager)
	wrapper.udpRelay.ProcessAsync()
	e.proxy, e.attempt = wrapper, generation
	e.router.set(wrapper)
	log.Infof(Category, "[Engine] transport attached generation=%d interface=%s", generation, e.ifaceName)
	return nil
}

// Detach fences admission before canceling pending work and closing flows. It
// never stops the packet stack or releases the network interface.
func (e *Engine) Detach(ctx context.Context, generation uint64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.attempt != generation || e.proxy == nil {
		return nil
	}
	return e.detachLocked(ctx)
}

func (e *Engine) detachLocked(ctx context.Context) error {
	if e.router != nil {
		e.router.set(nil)
	}
	if e.proxy == nil {
		return nil
	}
	e.proxy.flows.sealAndClose()
	if e.proxy.udpRelay != nil {
		e.proxy.udpRelay.Close()
	}
	if err := e.proxy.flows.wait(ctx); err != nil {
		return err
	}
	log.Infof(Category, "[Engine] transport detached generation=%d interface=%s", e.attempt, e.ifaceName)
	e.proxy = nil
	return nil
}

// Stop releases this handle's resources in reverse start order. A stale handle
// cannot stop a newer owner.
func (e *Engine) Stop(ctx context.Context) error {
	if e == nil {
		return nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stopped {
		return e.stopErr
	}
	stopPlatform := e.stopPlatform

	engineMu.Lock()
	if activeEngine != e {
		engineMu.Unlock()
		return nil
	}
	// Keep the reservation while cleanup runs, without making other sessions
	// wait on a proxy handshake merely to discover that this owner is busy.
	engineMu.Unlock()
	log.Debugf(Category, "[Engine] stopping owned tun2socks engine")
	if e.router != nil {
		e.router.close()
	}
	if e.proxy != nil {
		e.proxy.flows.sealAndClose()
	}
	var platformErr error
	if stopPlatform != nil {
		platformErr = stopPlatform(ctx)
	}
	var flowErr error
	if e.proxy != nil {
		flowErr = e.proxy.flows.wait(ctx)
	}
	e.stopErr = errors.Join(platformErr, flowErr)
	if e.stopErr != nil {
		return e.stopErr
	}
	e.stopped = true
	engineMu.Lock()
	if activeEngine == e {
		activeEngine = nil
	}
	engineMu.Unlock()
	log.Debugf(Category, "[Engine] owned tun2socks engine stopped")
	return e.stopErr
}

// InterfaceName is the exact platform interface owned by this engine.
func (e *Engine) InterfaceName() string {
	if e == nil {
		return ""
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.ifaceName
}

func (p *DobbyProxy) flowStats() string {
	return fmt.Sprintf(
		"activeTCP=%d peakTCP=%d activeUDP=%d peakUDP=%d tcpAttempt=%d udpAttempt=%d udpIdleTimeout=%d",
		p.activeTCP.Load(),
		p.peakTCP.Load(),
		p.activeUDP.Load(),
		p.peakUDP.Load(),
		p.tcpDialAttempt.Load(),
		p.udpDialAttempt.Load(),
		p.udpIdleTimeout.Load(),
	)
}

func updatePeakInt64(peak *atomic.Int64, current int64) {
	for {
		old := peak.Load()
		if current <= old || peak.CompareAndSwap(old, current) {
			return
		}
	}
}
