package tunnel

import (
	"context"
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
	mu        sync.Mutex
	isRunning bool
	statsStop chan struct{}
)

const (
	maxActiveTCPConnections   = 256
	maxActiveUDPAssociations  = 256
	udpAssociationIdleTimeout = 10 * time.Second
)

type DobbyProxy struct {
	vpn     proxy.Proxy
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
	route, px := "VPN", p.vpn
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
	route, px := "VPN", p.vpn
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
	return p.vpn.Addr()
}

func isBlockedIPv6Destination(metadata *M.Metadata) bool {
	return metadata != nil && metadata.DstIP.Is6() && !metadata.DstIP.Is4In6()
}

func (p *DobbyProxy) Proto() proto.Proto {
	return p.vpn.Proto()
}

func StartEngine(cfg platform_engine.EngineConfig) error {
	mu.Lock()
	defer mu.Unlock()

	if isRunning {
		log.Debugf(Category, "[Engine] StartEngine requested while already running; stopping previous engine first")
		stopLocked()
	}

	log.Debugf(Category, "[Engine] StartEngine config proxy=%s fd=%d uplinkIface=%s", cfg.ProxyAddr, cfg.FD, cfg.UplinkIface)
	err := platform_engine.StartPlatformEngine(cfg)
	if err != nil {
		log.Debugf(Category, "[Engine] StartPlatformEngine failed: %v", err)
		return err
	}
	log.Debugf(Category, "[Engine] StartPlatformEngine OK")

	t := tunnel.T()
	if t == nil {
		log.Debugf(Category, "[Engine] tunnel.T() is nil after engine start")
		return fmt.Errorf("tunnel not initialized after engine start")
	}

	currentDialer := t.Dialer()
	vpnOutbound, ok := currentDialer.(proxy.Proxy)
	if !ok {
		log.Debugf(Category, "[Engine] Current dialer is not a proxy (type=%T)", currentDialer)
		return fmt.Errorf("current dialer is not a proxy")
	}
	log.Debugf(Category, "[Engine] vpn outbound proxy type=%T addr=%s", vpnOutbound, vpnOutbound.Addr())
	log.Debugf(Category, "[Engine] DobbyProxy limits tcp=%d udp=%d idleTimeout=%s", maxActiveTCPConnections, maxActiveUDPAssociations, udpAssociationIdleTimeout)

	wrapper := &DobbyProxy{
		vpn:     vpnOutbound,
		direct:  &protected_dialer.ProtectedDirectProxy{Proxy: proxy.NewDirect()},
		tcpSlot: flowSlot{maxTotal: maxActiveTCPConnections},
		udpSlot: flowSlot{maxTotal: maxActiveUDPAssociations},
	}

	t.SetDialer(wrapper)
	log.Debugf(Category, "[Engine] DobbyProxy installed")
	statsStop = make(chan struct{})
	go wrapper.logStatsLoop(statsStop)
	isRunning = true
	return nil
}

func stopLocked() {
	log.Debugf(Category, "[Engine] stopping tun2socks engine")
	if statsStop != nil {
		close(statsStop)
		statsStop = nil
	}
	platform_engine.EngineStop()
	isRunning = false
	log.Debugf(Category, "[Engine] tun2socks engine stopped")
}

func StopEngine() {
	mu.Lock()
	defer mu.Unlock()

	if !isRunning {
		log.Debugf(Category, "[Engine] StopEngine skipped: not running")
		return
	}

	stopLocked()
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
