package tunnel

import (
	"context"
	"fmt"
	"net"
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
	maxActiveTCPConnections         = 96
	maxActiveTCPConnectionsPerHost  = 32
	maxActiveTCPConnectionsPerDest  = 16
	maxActiveUDPAssociations        = 64
	maxActiveUDPAssociationsPerHost = 16
	maxActiveUDPAssociationsPerDest = 8
	udpAssociationIdleTimeout       = 10 * time.Second
)

type DobbyProxy struct {
	vpn       proxy.Proxy
	direct    proxy.Proxy
	limiter   *flowLimiter
	activeTCP atomic.Int64
	activeUDP atomic.Int64
	peakTCP   atomic.Int64
	peakUDP   atomic.Int64

	tcpDialAttempt atomic.Uint64
	tcpDialOK      atomic.Uint64
	tcpDialErr     atomic.Uint64
	tcpLimitErr    atomic.Uint64
	udpDialAttempt atomic.Uint64
	udpDialOK      atomic.Uint64
	udpDialErr     atomic.Uint64
	udpLimitErr    atomic.Uint64
	udpIdleTimeout atomic.Uint64
}

type trackedConn struct {
	net.Conn
	route   string
	dest    string
	started time.Time
	release func() int64
	once    sync.Once
}

func (c *trackedConn) Close() error {
	var err error
	c.once.Do(func() {
		active := c.release()
		log.Infof("[Router] TCP closed route=%s dest=%s lifetime=%s activeTCP=%d", c.route, c.dest, time.Since(c.started), active)
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
		log.Infof("[Router] UDP closed route=%s dest=%s lifetime=%s activeUDP=%d", c.route, c.dest, time.Since(c.started), active)
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
	closed        bool
}

func newIdlePacketConn(conn net.PacketConn, timeout time.Duration, route string, dest string, onIdleTimeout func() uint64) *idlePacketConn {
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
	if c.timer != nil {
		c.timer.Stop()
	}
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

	count := uint64(0)
	if c.onIdleTimeout != nil {
		count = c.onIdleTimeout()
	}
	log.Infof("[Router] UDP idle timeout route=%s dest=%s timeout=%s count=%d", c.route, c.dest, c.timeout, count)
	_ = c.PacketConn.Close()
}

func (c *idlePacketConn) touch() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed && c.timer != nil {
		c.timer.Reset(c.timeout)
	}
}

func (p *DobbyProxy) DialContext(ctx context.Context, metadata *M.Metadata) (net.Conn, error) {
	start := time.Now()
	dest := metadata.DestinationAddress()
	attempt := p.tcpDialAttempt.Add(1)
	if IsBypass(metadata) {
		log.Infof("[Router] TCP dial attempt=%d route=DIRECT dstIP=%s dest=%s proto=%s stats={%s}", attempt, metadata.DstIP, dest, metadata.Network, p.flowStats())
		active, release, err := p.reserveTCP(dest)
		if err != nil {
			p.tcpDialErr.Add(1)
			p.tcpLimitErr.Add(1)
			log.Infof("[Router] DIRECT TCP dial error attempt=%d dest=%s elapsed=%s stats={%s} err=%v", attempt, dest, time.Since(start), p.flowStats(), err)
			return nil, err
		}
		conn, err := p.direct.DialContext(ctx, metadata)
		if err != nil {
			release()
			p.tcpDialErr.Add(1)
			log.Infof("[Router] DIRECT TCP dial error attempt=%d dest=%s elapsed=%s stats={%s} err=%v", attempt, dest, time.Since(start), p.flowStats(), err)
			return nil, err
		}
		p.tcpDialOK.Add(1)
		updatePeakInt64(&p.peakTCP, active)
		log.Infof("[Router] DIRECT TCP dial OK attempt=%d dest=%s elapsed=%s local=%s remote=%s stats={%s}", attempt, dest, time.Since(start), conn.LocalAddr(), conn.RemoteAddr(), p.flowStats())
		return &trackedConn{Conn: conn, release: release, route: "DIRECT", dest: dest, started: time.Now()}, nil
	}
	log.Infof("[Router] TCP dial attempt=%d route=VPN dstIP=%s dest=%s proto=%s stats={%s}", attempt, metadata.DstIP, dest, metadata.Network, p.flowStats())
	active, release, err := p.reserveTCP(dest)
	if err != nil {
		p.tcpDialErr.Add(1)
		p.tcpLimitErr.Add(1)
		log.Infof("[Router] VPN TCP dial error attempt=%d dest=%s elapsed=%s stats={%s} err=%v", attempt, dest, time.Since(start), p.flowStats(), err)
		return nil, err
	}
	conn, err := p.vpn.DialContext(ctx, metadata)
	if err != nil {
		release()
		p.tcpDialErr.Add(1)
		log.Infof("[Router] VPN TCP dial error attempt=%d dest=%s elapsed=%s stats={%s} err=%v", attempt, dest, time.Since(start), p.flowStats(), err)
		return nil, err
	}
	p.tcpDialOK.Add(1)
	updatePeakInt64(&p.peakTCP, active)
	log.Infof("[Router] VPN TCP dial OK attempt=%d dest=%s elapsed=%s local=%s remote=%s stats={%s}", attempt, dest, time.Since(start), conn.LocalAddr(), conn.RemoteAddr(), p.flowStats())
	return &trackedConn{Conn: conn, release: release, route: "VPN", dest: dest, started: time.Now()}, nil
}

func (p *DobbyProxy) DialUDP(metadata *M.Metadata) (net.PacketConn, error) {
	start := time.Now()
	dest := metadata.DestinationAddress()
	attempt := p.udpDialAttempt.Add(1)
	if IsBypass(metadata) {
		log.Infof("[Router] UDP dial attempt=%d route=DIRECT dstIP=%s dest=%s proto=%s stats={%s}", attempt, metadata.DstIP, dest, metadata.Network, p.flowStats())
		active, release, err := p.reserveUDP(dest)
		if err != nil {
			p.udpDialErr.Add(1)
			p.udpLimitErr.Add(1)
			log.Infof("[Router] DIRECT UDP dial error attempt=%d dest=%s elapsed=%s stats={%s} err=%v", attempt, dest, time.Since(start), p.flowStats(), err)
			return nil, err
		}
		conn, err := p.direct.DialUDP(metadata)
		if err != nil {
			release()
			p.udpDialErr.Add(1)
			log.Infof("[Router] DIRECT UDP dial error attempt=%d dest=%s elapsed=%s stats={%s} err=%v", attempt, dest, time.Since(start), p.flowStats(), err)
			return nil, err
		}
		p.udpDialOK.Add(1)
		updatePeakInt64(&p.peakUDP, active)
		log.Infof("[Router] DIRECT UDP dial OK attempt=%d dest=%s elapsed=%s local=%s stats={%s}", attempt, dest, time.Since(start), conn.LocalAddr(), p.flowStats())
		tracked := &trackedPacketConn{PacketConn: conn, release: release, route: "DIRECT", dest: dest, started: time.Now()}
		return newIdlePacketConn(tracked, udpAssociationIdleTimeout, "DIRECT", dest, func() uint64 {
			return p.udpIdleTimeout.Add(1)
		}), nil
	}
	log.Infof("[Router] UDP dial attempt=%d route=VPN dstIP=%s dest=%s proto=%s stats={%s}", attempt, metadata.DstIP, dest, metadata.Network, p.flowStats())
	active, release, err := p.reserveUDP(dest)
	if err != nil {
		p.udpDialErr.Add(1)
		p.udpLimitErr.Add(1)
		log.Infof("[Router] VPN UDP dial error attempt=%d dest=%s elapsed=%s stats={%s} err=%v", attempt, dest, time.Since(start), p.flowStats(), err)
		return nil, err
	}
	conn, err := p.vpn.DialUDP(metadata)
	if err != nil {
		release()
		p.udpDialErr.Add(1)
		log.Infof("[Router] VPN UDP dial error attempt=%d dest=%s elapsed=%s stats={%s} err=%v", attempt, dest, time.Since(start), p.flowStats(), err)
		return nil, err
	}
	p.udpDialOK.Add(1)
	updatePeakInt64(&p.peakUDP, active)
	log.Infof("[Router] VPN UDP dial OK attempt=%d dest=%s elapsed=%s local=%s stats={%s}", attempt, dest, time.Since(start), conn.LocalAddr(), p.flowStats())
	tracked := &trackedPacketConn{PacketConn: conn, release: release, route: "VPN", dest: dest, started: time.Now()}
	return newIdlePacketConn(tracked, udpAssociationIdleTimeout, "VPN", dest, func() uint64 {
		return p.udpIdleTimeout.Add(1)
	}), nil
}

func (p *DobbyProxy) Addr() string {
	return p.vpn.Addr()
}

func (p *DobbyProxy) Proto() proto.Proto {
	return p.vpn.Proto()
}

func StartEngine(cfg platform_engine.EngineConfig) error {
	mu.Lock()
	defer mu.Unlock()

	if isRunning {
		log.Infof("[Engine] StartEngine requested while already running; stopping previous engine first")
		stopLocked()
	}

	log.Infof("[Engine] StartEngine config proxy=%s fd=%d uplinkIface=%s", cfg.ProxyAddr, cfg.FD, cfg.UplinkIface)
	log.Infof("[Engine] StartEngine: calling StartPlatformEngine")
	err := platform_engine.StartPlatformEngine(cfg)
	if err != nil {
		log.Infof("[Engine] StartPlatformEngine failed: %v", err)
		return err
	}
	log.Infof("[Engine] StartPlatformEngine OK")

	t := tunnel.T()
	if t == nil {
		log.Infof("[Engine] tunnel.T() is nil after engine start")
		return fmt.Errorf("tunnel not initialized after engine start")
	}

	currentDialer := t.Dialer()
	vpnOutbound, ok := currentDialer.(proxy.Proxy)
	if !ok {
		log.Infof("[Engine] Current dialer is not a proxy (type=%T)", currentDialer)
		return fmt.Errorf("current dialer is not a proxy")
	}
	log.Infof("[Engine] vpn outbound proxy type=%T addr=%s", vpnOutbound, vpnOutbound.Addr())

	directOutbound := &protected_dialer.ProtectedDirectProxy{
		Proxy: proxy.NewDirect(),
	}

	wrapper := &DobbyProxy{
		vpn:     vpnOutbound,
		direct:  directOutbound,
		limiter: newFlowLimiter(),
	}

	t.SetDialer(wrapper)
	log.Infof("[Engine] DobbyProxy installed")
	statsStop = make(chan struct{})
	go wrapper.logStatsLoop(statsStop)
	isRunning = true
	return nil
}

func stopLocked() {
	log.Infof("[Engine] stopping tun2socks engine")
	if statsStop != nil {
		close(statsStop)
		statsStop = nil
	}
	platform_engine.EngineStop()
	isRunning = false
	log.Infof("[Engine] tun2socks engine stopped")
}

func StopEngine() {
	mu.Lock()
	defer mu.Unlock()

	if !isRunning {
		log.Infof("[Engine] StopEngine skipped: not running")
		return
	}

	stopLocked()
}

func (p *DobbyProxy) flowStats() string {
	return fmt.Sprintf(
		"activeTCP=%d peakTCP=%d activeUDP=%d peakUDP=%d tcp=%d/%d/%d udp=%d/%d/%d tcpLimit=%d udpLimit=%d udpIdleTimeout=%d",
		p.activeTCP.Load(),
		p.peakTCP.Load(),
		p.activeUDP.Load(),
		p.peakUDP.Load(),
		p.tcpDialAttempt.Load(),
		p.tcpDialOK.Load(),
		p.tcpDialErr.Load(),
		p.udpDialAttempt.Load(),
		p.udpDialOK.Load(),
		p.udpDialErr.Load(),
		p.tcpLimitErr.Load(),
		p.udpLimitErr.Load(),
		p.udpIdleTimeout.Load(),
	)
}

func (p *DobbyProxy) logStatsLoop(stop <-chan struct{}) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			log.Infof("[Router STATS] flow={%s}", p.flowStats())
		case <-stop:
			log.Infof("[Router STATS] stopped flow={%s}", p.flowStats())
			return
		}
	}
}

func updatePeakInt64(peak *atomic.Int64, current int64) {
	for {
		old := peak.Load()
		if current <= old || peak.CompareAndSwap(old, current) {
			return
		}
	}
}

type flowLimiter struct {
	mu      sync.Mutex
	tcpHost map[string]int
	tcpDest map[string]int
	udpHost map[string]int
	udpDest map[string]int
}

func newFlowLimiter() *flowLimiter {
	return &flowLimiter{
		tcpHost: make(map[string]int),
		tcpDest: make(map[string]int),
		udpHost: make(map[string]int),
		udpDest: make(map[string]int),
	}
}

func (p *DobbyProxy) reserveTCP(dest string) (int64, func() int64, error) {
	return p.limiter.reserve(
		dest,
		&p.activeTCP,
		p.limiter.tcpHost,
		p.limiter.tcpDest,
		maxActiveTCPConnections,
		maxActiveTCPConnectionsPerHost,
		maxActiveTCPConnectionsPerDest,
	)
}

func (p *DobbyProxy) reserveUDP(dest string) (int64, func() int64, error) {
	return p.limiter.reserve(
		dest,
		&p.activeUDP,
		p.limiter.udpHost,
		p.limiter.udpDest,
		maxActiveUDPAssociations,
		maxActiveUDPAssociationsPerHost,
		maxActiveUDPAssociationsPerDest,
	)
}

func (l *flowLimiter) reserve(
	dest string,
	active *atomic.Int64,
	hostCounts map[string]int,
	destCounts map[string]int,
	maxActive int64,
	maxPerHost int,
	maxPerDest int,
) (int64, func() int64, error) {
	host := flowHost(dest)

	l.mu.Lock()
	defer l.mu.Unlock()

	currentActive := active.Load()
	switch {
	case currentActive >= maxActive:
		return currentActive, nil, fmt.Errorf("flow limit reached active=%d max=%d", currentActive, maxActive)
	case hostCounts[host] >= maxPerHost:
		return currentActive, nil, fmt.Errorf("host flow limit reached host=%s active=%d max=%d", host, hostCounts[host], maxPerHost)
	case destCounts[dest] >= maxPerDest:
		return currentActive, nil, fmt.Errorf("destination flow limit reached dest=%s active=%d max=%d", dest, destCounts[dest], maxPerDest)
	}

	hostCounts[host]++
	destCounts[dest]++
	currentActive = active.Add(1)

	release := func() int64 {
		l.mu.Lock()
		defer l.mu.Unlock()
		decrementFlowCount(hostCounts, host)
		decrementFlowCount(destCounts, dest)
		return active.Add(-1)
	}

	return currentActive, release, nil
}

func decrementFlowCount(counts map[string]int, key string) {
	if counts[key] <= 1 {
		delete(counts, key)
		return
	}
	counts[key]--
}

func flowHost(dest string) string {
	host, _, err := net.SplitHostPort(dest)
	if err != nil || host == "" {
		if dest == "" {
			return "(unknown)"
		}
		return dest
	}
	return host
}

