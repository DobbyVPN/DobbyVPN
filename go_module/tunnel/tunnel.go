package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	"github.com/xjasonlyu/tun2socks/v2/proxy"
	"github.com/xjasonlyu/tun2socks/v2/proxy/proto"
	"github.com/xjasonlyu/tun2socks/v2/tunnel"

	"go_module/log"
	"go_module/tunnel/platform_engine"
	"go_module/tunnel/protected_dialer"
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
	ready     bool
	stopped   bool
	stopErr   error
	statsStop chan struct{}
	ifaceName string

	// stopPlatform exists so ownership bookkeeping can be tested without a
	// real TUN device. Production handles use platform_engine.EngineStop.
	stopPlatform func() error
}

// Ready reports whether this handle owns a fully initialized tun2socks engine.
func (e *Engine) Ready() bool {
	if e == nil {
		return false
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.ready && !e.stopped
}

const (
	maxActiveTCPConnections   = 256
	maxActiveUDPAssociations  = 256
	udpAssociationIdleTimeout = 10 * time.Second
)

type DobbyProxy struct {
	vpn     proxy.Proxy
	vpnMu   sync.RWMutex
	direct  proxy.Proxy
	tcpSlot flowSlot
	udpSlot flowSlot

	activeTCP atomic.Int64
	activeUDP atomic.Int64
	peakTCP   atomic.Int64
	peakUDP   atomic.Int64

	tcpDialAttempt atomic.Uint64
	tcpLimitErr    atomic.Uint64
	udpDialAttempt atomic.Uint64
	udpLimitErr    atomic.Uint64
	udpIdleTimeout atomic.Uint64
}

type trackedConn struct {
	net.Conn
	route      string
	dest       string
	started    time.Time
	release    func() int64
	once       sync.Once
	writeMu    sync.Mutex
	lastWrite  time.Time
	rttSamples []time.Duration
}

func (c *trackedConn) Write(b []byte) (int, error) {
	c.writeMu.Lock()
	c.lastWrite = time.Now()
	c.writeMu.Unlock()
	return c.Conn.Write(b)
}

func (c *trackedConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.writeMu.Lock()
		lw := c.lastWrite
		c.writeMu.Unlock()
		if !lw.IsZero() {
			rtt := time.Since(lw)
			c.writeMu.Lock()
			c.lastWrite = time.Time{}
			c.rttSamples = append(c.rttSamples, rtt)
			c.writeMu.Unlock()
		}
	}
	return n, err
}

func (c *trackedConn) Close() error {
	var err error
	c.once.Do(func() {
		active := c.release()
		c.writeMu.Lock()
		samples := c.rttSamples
		c.writeMu.Unlock()
		rttInfo := ""
		if len(samples) > 0 {
			var sum time.Duration
			minRTT, maxRTT := samples[0], samples[0]
			for _, s := range samples {
				sum += s
				if s < minRTT {
					minRTT = s
				}
				if s > maxRTT {
					maxRTT = s
				}
			}
			avg := sum / time.Duration(len(samples))
			rttInfo = fmt.Sprintf(" rtt(app): samples=%d min=%s avg=%s max=%s", len(samples), minRTT, avg, maxRTT)
		}
		log.Debugf(Category, "[Router] TCP closed route=%s dest=%s lifetime=%s activeTCP=%d%s", c.route, c.dest, time.Since(c.started), active, rttInfo)
		err = c.Conn.Close()
	})
	return err
}

type trackedPacketConn struct {
	net.PacketConn
	route   string
	dest    string
	started time.Time
	release func() int64
	once    sync.Once
}

func (c *trackedPacketConn) Close() error {
	var err error
	c.once.Do(func() {
		active := c.release()
		log.Debugf(Category, "[Router] UDP closed route=%s dest=%s lifetime=%s activeUDP=%d", c.route, c.dest, time.Since(c.started), active)
		err = c.PacketConn.Close()
	})
	return err
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
}

func newIdlePacketConn(conn net.PacketConn, timeout time.Duration, route, dest string, onIdleTimeout func() uint64) *idlePacketConn {
	c := &idlePacketConn{
		PacketConn:    conn,
		timeout:       timeout,
		route:         route,
		dest:          dest,
		onIdleTimeout: onIdleTimeout,
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
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.timer.Stop()
	c.mu.Unlock()
	return c.PacketConn.Close()
}

func (c *idlePacketConn) closeAfterIdleTimeout() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.mu.Unlock()

	var count uint64
	if c.onIdleTimeout != nil {
		count = c.onIdleTimeout()
	}
	log.Debugf(Category, "[Router] UDP idle timeout route=%s dest=%s timeout=%s count=%d", c.route, c.dest, c.timeout, count)
	_ = c.PacketConn.Close()
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
	if isBlockedIPv6Destination(metadata) {
		err := fmt.Errorf("IPv6 destination blocked: %s", dest)
		log.Debugf(Category, "[Router] TCP IPv6 blocked attempt=%d dstIP=%s dest=%s proto=%s stats={%s}", attempt, metadata.DstIP, dest, metadata.Network, p.flowStats())
		return nil, err
	}
	route, px := "VPN", p.currentVPNProxy()
	if IsBypass(metadata) {
		route, px = "DIRECT", p.direct
	}
	log.Debugf(Category, "[Router] TCP dial attempt=%d route=%s dstIP=%s dest=%s proto=%s stats={%s}", attempt, route, metadata.DstIP, dest, metadata.Network, p.flowStats())
	return p.dialTCPRoute(ctx, metadata, route, px, attempt, dest, start)
}

func (p *DobbyProxy) dialTCPRoute(ctx context.Context, metadata *M.Metadata, route string, px proxy.Proxy, attempt uint64, dest string, start time.Time) (net.Conn, error) {
	active, release, err := p.tcpSlot.reserve(&p.activeTCP)
	if err != nil {
		p.tcpLimitErr.Add(1)
		log.Debugf(Category, "[Router] %s TCP dial error attempt=%d dest=%s elapsed=%s stats={%s} err=%v", route, attempt, dest, time.Since(start), p.flowStats(), err)
		return nil, err
	}
	conn, err := px.DialContext(ctx, metadata)
	if err != nil {
		release()
		log.Debugf(Category, "[Router] %s TCP dial error attempt=%d dest=%s elapsed=%s stats={%s} err=%v", route, attempt, dest, time.Since(start), p.flowStats(), err)
		return nil, err
	}
	updatePeakInt64(&p.peakTCP, active)
	log.Debugf(Category, "[Router] %s TCP dial OK attempt=%d dest=%s elapsed=%s local=%s remote=%s stats={%s}", route, attempt, dest, time.Since(start), conn.LocalAddr(), conn.RemoteAddr(), p.flowStats())
	return &trackedConn{Conn: conn, release: release, route: route, dest: dest, started: time.Now()}, nil
}

func (p *DobbyProxy) DialUDP(metadata *M.Metadata) (net.PacketConn, error) {
	start := time.Now()
	dest := metadata.DestinationAddress()
	attempt := p.udpDialAttempt.Add(1)
	if isBlockedIPv6Destination(metadata) {
		err := fmt.Errorf("IPv6 destination blocked: %s", dest)
		log.Debugf(Category, "[Router] UDP IPv6 blocked attempt=%d dstIP=%s dest=%s proto=%s stats={%s}", attempt, metadata.DstIP, dest, metadata.Network, p.flowStats())
		return nil, err
	}
	route, px := "VPN", p.currentVPNProxy()
	if IsBypass(metadata) {
		route, px = "DIRECT", p.direct
	}
	log.Debugf(Category, "[Router] UDP dial attempt=%d route=%s dstIP=%s dest=%s proto=%s stats={%s}", attempt, route, metadata.DstIP, dest, metadata.Network, p.flowStats())
	return p.dialUDPRoute(metadata, route, px, attempt, dest, start)
}

func (p *DobbyProxy) dialUDPRoute(metadata *M.Metadata, route string, px proxy.Proxy, attempt uint64, dest string, start time.Time) (net.PacketConn, error) {
	active, release, err := p.udpSlot.reserve(&p.activeUDP)
	if err != nil {
		p.udpLimitErr.Add(1)
		log.Debugf(Category, "[Router] %s UDP dial error attempt=%d dest=%s elapsed=%s stats={%s} err=%v", route, attempt, dest, time.Since(start), p.flowStats(), err)
		return nil, err
	}
	conn, err := px.DialUDP(metadata)
	if err != nil {
		release()
		log.Debugf(Category, "[Router] %s UDP dial error attempt=%d dest=%s elapsed=%s stats={%s} err=%v", route, attempt, dest, time.Since(start), p.flowStats(), err)
		return nil, err
	}
	updatePeakInt64(&p.peakUDP, active)
	log.Debugf(Category, "[Router] %s UDP dial OK attempt=%d dest=%s elapsed=%s local=%s stats={%s}", route, attempt, dest, time.Since(start), conn.LocalAddr(), p.flowStats())
	tracked := &trackedPacketConn{PacketConn: conn, release: release, route: route, dest: dest, started: time.Now()}
	return newIdlePacketConn(tracked, udpAssociationIdleTimeout, route, dest, func() uint64 {
		return p.udpIdleTimeout.Add(1)
	}), nil
}

func (p *DobbyProxy) Addr() string {
	return p.currentVPNProxy().Addr()
}

func isBlockedIPv6Destination(metadata *M.Metadata) bool {
	return metadata != nil && metadata.DstIP.Is6() && !metadata.DstIP.Is4In6()
}

func (p *DobbyProxy) Proto() proto.Proto {
	return p.currentVPNProxy().Proto()
}

func (p *DobbyProxy) currentVPNProxy() proxy.Proxy {
	p.vpnMu.RLock()
	defer p.vpnMu.RUnlock()
	return p.vpn
}

// StartOwnedEngine starts tun2socks and returns the handle which exclusively
// owns the resulting process-global engine. A second start is rejected rather
// than stopping or reconfiguring the current session.
func StartOwnedEngine(cfg platform_engine.EngineConfig) (*Engine, error) {
	engineMu.Lock()
	defer engineMu.Unlock()

	if activeEngine != nil {
		return nil, ErrEngineBusy
	}
	handle, _, err := startOwnedEngineLocked(cfg)
	return handle, err
}

// startOwnedEngineLocked reports whether the platform engine accepted its
// configuration. Once accepted, that platform engine exclusively owns every
// resource embedded in cfg, including an fd-backed device, and its stop path
// releases those resources on both later startup failure and normal shutdown.
// Before acceptance, ownership remains with the caller.
func startOwnedEngineLocked(cfg platform_engine.EngineConfig) (*Engine, bool, error) {

	handle := &Engine{stopPlatform: platform_engine.EngineStop}
	// Reserve ownership before touching the platform engine. This closes the
	// race where two starts both observe an idle global engine.
	activeEngine = handle

	log.Debugf(Category, "[Engine] StartOwnedEngine config proxy=%s fd=%d uplinkIface=%s", cfg.ProxyAddr, cfg.FD, cfg.UplinkIface)
	if err := platform_engine.StartPlatformEngine(cfg); err != nil {
		activeEngine = nil
		log.Debugf(Category, "[Engine] StartPlatformEngine failed: %v", err)
		return nil, false, err
	}
	handle.ifaceName = platform_engine.InterfaceName()

	t := tunnel.T()
	if t == nil {
		cleanupErr := handle.stopPlatform()
		activeEngine = nil
		return nil, true, errors.Join(fmt.Errorf("tunnel not initialized after engine start"), cleanupErr)
	}

	vpnOutbound, ok := t.Dialer().(proxy.Proxy)
	if !ok {
		cleanupErr := handle.stopPlatform()
		activeEngine = nil
		return nil, true, errors.Join(fmt.Errorf("current dialer is not a proxy (type=%T)", t.Dialer()), cleanupErr)
	}

	wrapper := &DobbyProxy{
		vpn:     vpnOutbound,
		direct:  &protected_dialer.ProtectedDirectProxy{Proxy: proxy.NewDirect()},
		tcpSlot: flowSlot{maxTotal: maxActiveTCPConnections},
		udpSlot: flowSlot{maxTotal: maxActiveUDPAssociations},
	}
	t.SetDialer(wrapper)

	handle.statsStop = make(chan struct{})
	handle.ready = true
	log.Debugf(Category, "[Engine] DobbyProxy installed; owner is ready")
	go wrapper.logStatsLoop(handle.statsStop)
	return handle, true, nil
}

// Stop releases this handle's resources in reverse start order. A stale handle
// cannot stop a newer owner.
func (e *Engine) Stop() error {
	if e == nil {
		return nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stopped {
		return e.stopErr
	}
	e.stopped = true
	e.ready = false
	statsStop := e.statsStop
	e.statsStop = nil
	stopPlatform := e.stopPlatform

	engineMu.Lock()
	defer engineMu.Unlock()
	if activeEngine != e {
		return nil
	}
	if statsStop != nil {
		close(statsStop)
	}
	log.Debugf(Category, "[Engine] stopping owned tun2socks engine")
	if stopPlatform != nil {
		e.stopErr = stopPlatform()
	}
	activeEngine = nil
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
		"activeTCP=%d peakTCP=%d activeUDP=%d peakUDP=%d tcpAttempt=%d udpAttempt=%d tcpLimitErr=%d udpLimitErr=%d udpIdleTimeout=%d limits=tcp:%d,udp:%d",
		p.activeTCP.Load(),
		p.peakTCP.Load(),
		p.activeUDP.Load(),
		p.peakUDP.Load(),
		p.tcpDialAttempt.Load(),
		p.udpDialAttempt.Load(),
		p.tcpLimitErr.Load(),
		p.udpLimitErr.Load(),
		p.udpIdleTimeout.Load(),
		maxActiveTCPConnections,
		maxActiveUDPAssociations,
	)
}

func (p *DobbyProxy) logStatsLoop(stop <-chan struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	log.Debugf(Category, "[Router STATS] started interval=1s flow={%s} runtime={%s}", p.flowStats(), goRuntimeStats())
	for {
		select {
		case <-ticker.C:
			log.Debugf(Category, "[Router STATS] flow={%s} runtime={%s}", p.flowStats(), goRuntimeStats())
		case <-stop:
			log.Debugf(Category, "[Router STATS] stopped flow={%s} runtime={%s}", p.flowStats(), goRuntimeStats())
			return
		}
	}
}

func goRuntimeStats() string {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	return fmt.Sprintf(
		"goroutines=%d heapAllocMB=%.2f heapInuseMB=%.2f stackInuseMB=%.2f sysMB=%.2f nextGCMB=%.2f numGC=%d pauseTotalMs=%d gcCPUFraction=%.4f",
		runtime.NumGoroutine(),
		bytesToMiB(mem.HeapAlloc),
		bytesToMiB(mem.HeapInuse),
		bytesToMiB(mem.StackInuse),
		bytesToMiB(mem.Sys),
		bytesToMiB(mem.NextGC),
		mem.NumGC,
		mem.PauseTotalNs/uint64(time.Millisecond),
		mem.GCCPUFraction,
	)
}

func bytesToMiB(bytes uint64) float64 {
	return float64(bytes) / 1024.0 / 1024.0
}

func updatePeakInt64(peak *atomic.Int64, current int64) {
	for {
		old := peak.Load()
		if current <= old || peak.CompareAndSwap(old, current) {
			return
		}
	}
}

type flowSlot struct {
	maxTotal int64
}

func (s *flowSlot) reserve(active *atomic.Int64) (cur int64, release func() int64, err error) {
	cur = active.Add(1)
	if cur > s.maxTotal {
		active.Add(-1)
		return cur - 1, nil, fmt.Errorf("flow limit reached active=%d max=%d", cur-1, s.maxTotal)
	}
	return cur, func() int64 { return active.Add(-1) }, nil
}
