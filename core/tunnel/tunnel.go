package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"runtime"
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
	engineMu              sync.Mutex
	activeEngine          *Engine
	stopOperationSequence atomic.Uint64
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
	stopPlatform      func(context.Context) error
	stopAfter         func(time.Duration, func()) func() bool
	captureStopStacks func() ([]byte, bool, error)
}

const (
	udpAssociationIdleTimeout = 10 * time.Second
	flowProtocolTCP           = "tcp"
	flowProtocolUDP           = "udp"
)

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

type flowIdentity struct {
	generation uint64
	attempt    uint64
	protocol   string
	family     string
	route      string
	dest       string
}

func (id flowIdentity) String() string {
	return fmt.Sprintf("generation=%d protocol=%s attempt=%d family=%s route=%s dest=%s", id.generation, id.protocol, id.attempt, id.family, id.route, id.dest)
}

func logicalFamily(metadata *M.Metadata) string {
	ip := metadata.DstIP.Unmap()
	if ip.Is4() {
		return "ipv4"
	}
	if ip.Is6() {
		return "ipv6"
	}
	return "unknown"
}

func (t *trafficCounter) observeRead(n int, err error, started time.Time, identity flowIdentity) {
	if elapsed, first := t.recordRead(n, started); first {
		log.Debugf(Category, "[Router] first downstream response %s bytes=%d elapsed=%s", identity, n, elapsed)
	}
	if err != nil && t.readError.CompareAndSwap(false, true) {
		log.Debugf(Category, "[Router] read terminal error %s err=%v", identity, err)
	}
}

func (t *trafficCounter) observeWrite(n int, err error, identity flowIdentity) {
	t.recordWrite(n)
	if err != nil && t.writeError.CompareAndSwap(false, true) {
		log.Debugf(Category, "[Router] write terminal error %s err=%v", identity, err)
	}
}

type trackedConn struct {
	net.Conn
	route      string
	dest       string
	generation uint64
	attempt    uint64
	family     string
	started    time.Time
	release    func() int64
	mu         sync.Mutex
	closed     bool
	owner      *flowOwner
	closeErr   error
	traffic    trafficCounter
}

func (c *trackedConn) identity() flowIdentity {
	return flowIdentity{c.generation, c.attempt, flowProtocolTCP, c.family, c.route, c.dest}
}

func (c *trackedConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	c.traffic.observeRead(n, err, c.started, c.identity())
	return n, err
}

func (c *trackedConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	c.traffic.observeWrite(n, err, c.identity())
	return n, err
}

func (c *trackedConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	log.Debugf(Category, "[Router] TCP close begin %s", c.identity())
	c.closeErr = c.Conn.Close()
	if c.closeErr != nil && !errors.Is(c.closeErr, net.ErrClosed) {
		if c.owner != nil {
			c.owner.release(c, c.closeErr)
		}
		log.Errorf(Category, "[Router] TCP close failed %s lifetime=%s %s err=%v", c.identity(), time.Since(c.started), c.traffic.summary(), c.closeErr)
		return c.closeErr
	}
	c.closed, c.closeErr = true, nil
	active := c.release()
	if c.owner != nil {
		c.owner.release(c, nil)
	}
	log.Debugf(Category, "[Router] TCP closed %s lifetime=%s %s activeTCP=%d", c.identity(), time.Since(c.started), c.traffic.summary(), active)
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
	route      string
	dest       string
	generation uint64
	attempt    uint64
	family     string
	traffic    trafficCounter
	started    time.Time
	release    func() int64
	mu         sync.Mutex
	closed     bool
	closeErr   error
}

func (c *trackedPacketConn) identity() flowIdentity {
	return flowIdentity{c.generation, c.attempt, flowProtocolUDP, c.family, c.route, c.dest}
}

func (c *trackedPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, address, err := c.PacketConn.ReadFrom(b)
	c.traffic.observeRead(n, err, c.started, c.identity())
	return n, address, err
}

func (c *trackedPacketConn) WriteTo(b []byte, address net.Addr) (int, error) {
	n, err := c.PacketConn.WriteTo(b, address)
	c.traffic.observeWrite(n, err, c.identity())
	return n, err
}

func (c *trackedPacketConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	log.Debugf(Category, "[Router] UDP close begin %s", c.identity())
	c.closeErr = c.PacketConn.Close()
	if c.closeErr != nil && !errors.Is(c.closeErr, net.ErrClosed) {
		log.Errorf(Category, "[Router] UDP close failed %s lifetime=%s %s err=%v", c.identity(), time.Since(c.started), c.traffic.summary(), c.closeErr)
		return c.closeErr
	}
	c.closed, c.closeErr = true, nil
	active := c.release()
	log.Debugf(Category, "[Router] UDP closed %s lifetime=%s %s activeUDP=%d", c.identity(), time.Since(c.started), c.traffic.summary(), active)
	return nil
}

type idlePacketConn struct {
	net.PacketConn
	timeout       time.Duration
	timer         *time.Timer
	route         string
	dest          string
	identity      flowIdentity
	onIdleTimeout func() uint64
	mu            sync.Mutex
	lastTouch     time.Time
	closed        bool
	closeErr      error
	owner         *flowOwner
}

func newIdlePacketConn(conn net.PacketConn, timeout time.Duration, identity flowIdentity, onIdleTimeout func() uint64, owner *flowOwner) *idlePacketConn {
	c := &idlePacketConn{
		PacketConn:    conn,
		timeout:       timeout,
		route:         identity.route,
		dest:          identity.dest,
		identity:      identity,
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
	log.Debugf(Category, "[Router] UDP idle timeout %s timeout=%s count=%d", c.identity, c.timeout, count)
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
	log.Debugf(Category, "[Router] TCP dial begin %s stats={%s}", flowIdentity{p.generation, attempt, flowProtocolTCP, logicalFamily(metadata), route, dest}, p.flowStats())
	return p.dialTCPRoute(ctx, metadata, route, px, attempt, dest, start)
}

func (p *DobbyProxy) dialTCPRoute(ctx context.Context, metadata *M.Metadata, route string, px proxy.Proxy, attempt uint64, dest string, start time.Time) (net.Conn, error) {
	if err := p.flows.beginDial(); err != nil {
		log.Debugf(Category, "[Router] TCP dial error %s elapsed=%s err=%v", flowIdentity{p.generation, attempt, flowProtocolTCP, logicalFamily(metadata), route, dest}, time.Since(start), err)
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
		log.Debugf(Category, "[Router] TCP dial error %s elapsed=%s stats={%s} err=%v", flowIdentity{p.generation, attempt, flowProtocolTCP, logicalFamily(metadata), route, dest}, time.Since(start), p.flowStats(), err)
		return nil, err
	}
	log.Debugf(Category, "[Router] TCP dial OK %s elapsed=%s local=%s remote=%s stats={%s}", flowIdentity{p.generation, attempt, flowProtocolTCP, logicalFamily(metadata), route, dest}, time.Since(start), conn.LocalAddr(), conn.RemoteAddr(), p.flowStats())
	tracked := &trackedConn{Conn: conn, release: release, route: route, dest: dest, generation: p.generation, attempt: attempt, family: logicalFamily(metadata), started: time.Now(), owner: &p.flows}
	if !p.flows.track(tracked) {
		closeErr := tracked.Close()
		log.Debugf(Category, "[Router] late TCP dial closed %s elapsed=%s err=%v", tracked.identity(), time.Since(start), closeErr)
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
	log.Debugf(Category, "[Router] UDP dial begin %s stats={%s}", flowIdentity{p.generation, attempt, flowProtocolUDP, logicalFamily(metadata), route, dest}, p.flowStats())
	return p.dialUDPRoute(metadata, route, px, attempt, dest, start)
}

func (p *DobbyProxy) dialUDPRoute(metadata *M.Metadata, route string, px proxy.Proxy, attempt uint64, dest string, start time.Time) (net.PacketConn, error) {
	if err := p.flows.beginDial(); err != nil {
		log.Debugf(Category, "[Router] UDP dial error %s elapsed=%s err=%v", flowIdentity{p.generation, attempt, flowProtocolUDP, logicalFamily(metadata), route, dest}, time.Since(start), err)
		return nil, err
	}
	defer p.flows.endDial()
	active := p.activeUDP.Add(1)
	release := func() int64 { return p.activeUDP.Add(-1) }
	updatePeakInt64(&p.peakUDP, active)
	conn, err := px.DialUDP(metadata)
	if err != nil {
		release()
		log.Debugf(Category, "[Router] UDP dial error %s elapsed=%s stats={%s} err=%v", flowIdentity{p.generation, attempt, flowProtocolUDP, logicalFamily(metadata), route, dest}, time.Since(start), p.flowStats(), err)
		return nil, err
	}
	log.Debugf(Category, "[Router] UDP dial OK %s elapsed=%s local=%s stats={%s}", flowIdentity{p.generation, attempt, flowProtocolUDP, logicalFamily(metadata), route, dest}, time.Since(start), conn.LocalAddr(), p.flowStats())
	tracked := &trackedPacketConn{PacketConn: conn, release: release, route: route, dest: dest, generation: p.generation, attempt: attempt, family: logicalFamily(metadata), started: time.Now()}
	owned := newIdlePacketConn(tracked, udpAssociationIdleTimeout, tracked.identity(), func() uint64 {
		return p.udpIdleTimeout.Add(1)
	}, &p.flows)
	owned.mu.Lock()
	accepted := !owned.closed && p.flows.track(owned)
	owned.mu.Unlock()
	if !accepted {
		closeErr := owned.Close()
		log.Debugf(Category, "[Router] late UDP dial closed %s elapsed=%s err=%v", tracked.identity(), time.Since(start), closeErr)
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
	started := time.Now()
	generation, iface := e.attempt, e.ifaceName
	operation := stopOperationSequence.Add(1)
	var phase atomic.Value
	phase.Store("starting")
	after := e.stopAfter
	if after == nil {
		after = func(delay time.Duration, callback func()) func() bool {
			timer := time.AfterFunc(delay, callback)
			return timer.Stop
		}
	}
	capture := e.captureStopStacks
	if capture == nil {
		capture = func() ([]byte, bool, error) {
			buffer := make([]byte, 64*1024)
			n := runtime.Stack(buffer, true)
			if n == 0 {
				return nil, false, errors.New("runtime stack capture returned no data")
			}
			return buffer[:n], n == len(buffer), nil
		}
	}
	var diagnosticOnce sync.Once
	stopTimer := after(3*time.Second, func() {
		diagnosticOnce.Do(func() {
			stage := phase.Load().(string)
			log.Warnf(Category, "[Engine] stop slow operation=%d generation=%d interface=%s phase=%s elapsed=%s contextError=%v", operation, generation, iface, stage, time.Since(started), ctx.Err())
			stacks, truncated, err := capture()
			if len(stacks) > 64*1024 {
				stacks = stacks[:64*1024]
				truncated = true
			}
			if err != nil {
				log.Errorf(Category, "[Engine] stop stack capture failed operation=%d generation=%d interface=%s phase=%s truncated=%t err=%v", operation, generation, iface, stage, truncated, err)
				if len(stacks) == 0 {
					return
				}
			}
			log.Warnf(Category, "[Engine] stop stacks operation=%d generation=%d interface=%s phase=%s truncated=%t\n%s", operation, generation, iface, stage, truncated, stacks)
		})
	})
	defer stopTimer()
	runPhase := func(name string, fn func() error) error {
		phase.Store(name)
		phaseStarted := time.Now()
		log.Infof(Category, "[Engine] stop phase=%s started operation=%d generation=%d interface=%s", name, operation, generation, iface)
		err := fn()
		if err != nil {
			log.Errorf(Category, "[Engine] stop phase=%s failed operation=%d generation=%d interface=%s elapsed=%s err=%v", name, operation, generation, iface, time.Since(phaseStarted), err)
		} else {
			log.Infof(Category, "[Engine] stop phase=%s finished operation=%d generation=%d interface=%s elapsed=%s", name, operation, generation, iface, time.Since(phaseStarted))
		}
		return err
	}
	log.Infof(Category, "[Engine] stopping owned tun2socks engine operation=%d generation=%d interface=%s", operation, generation, iface)
	if e.router != nil {
		_ = runPhase("router-close", func() error { e.router.close(); return nil })
	}
	if e.proxy != nil {
		_ = runPhase("flow-close", func() error { e.proxy.flows.sealAndClose(); return nil })
	}
	var platformErr error
	if stopPlatform != nil {
		platformErr = runPhase("platform-stop", func() error { return stopPlatform(ctx) })
	}
	var flowErr error
	if e.proxy != nil {
		flowErr = runPhase("flow-wait", func() error { return e.proxy.flows.wait(ctx) })
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
	log.Infof(Category, "[Engine] owned tun2socks engine stopped operation=%d generation=%d interface=%s", operation, generation, iface)
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
