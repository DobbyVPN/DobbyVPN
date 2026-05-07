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
	vpn     proxy.Proxy
	direct  proxy.Proxy
	tcpSlot flowSlot
	udpSlot flowSlot

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
	log.Infof("[Router] UDP idle timeout route=%s dest=%s timeout=%s count=%d", c.route, c.dest, c.timeout, count)
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
	route, px := "VPN", proxy.Proxy(p.vpn)
	if IsBypass(metadata) {
		route, px = "DIRECT", p.direct
	}
	log.Infof("[Router] TCP dial attempt=%d route=%s dstIP=%s dest=%s proto=%s stats={%s}", attempt, route, metadata.DstIP, dest, metadata.Network, p.flowStats())
	return p.dialTCPRoute(ctx, metadata, route, px, attempt, dest, start)
}

func (p *DobbyProxy) dialTCPRoute(ctx context.Context, metadata *M.Metadata, route string, px proxy.Proxy, attempt uint64, dest string, start time.Time) (net.Conn, error) {
	active, release, err := p.tcpSlot.reserve(dest, &p.activeTCP)
	if err != nil {
		p.tcpDialErr.Add(1)
		p.tcpLimitErr.Add(1)
		log.Infof("[Router] %s TCP dial error attempt=%d dest=%s elapsed=%s stats={%s} err=%v", route, attempt, dest, time.Since(start), p.flowStats(), err)
		return nil, err
	}
	conn, err := px.DialContext(ctx, metadata)
	if err != nil {
		release()
		p.tcpDialErr.Add(1)
		log.Infof("[Router] %s TCP dial error attempt=%d dest=%s elapsed=%s stats={%s} err=%v", route, attempt, dest, time.Since(start), p.flowStats(), err)
		return nil, err
	}
	p.tcpDialOK.Add(1)
	updatePeakInt64(&p.peakTCP, active)
	log.Infof("[Router] %s TCP dial OK attempt=%d dest=%s elapsed=%s local=%s remote=%s stats={%s}", route, attempt, dest, time.Since(start), conn.LocalAddr(), conn.RemoteAddr(), p.flowStats())
	return &trackedConn{Conn: conn, release: release, route: route, dest: dest, started: time.Now()}, nil
}

func (p *DobbyProxy) DialUDP(metadata *M.Metadata) (net.PacketConn, error) {
	start := time.Now()
	dest := metadata.DestinationAddress()
	attempt := p.udpDialAttempt.Add(1)
	route, px := "VPN", proxy.Proxy(p.vpn)
	if IsBypass(metadata) {
		route, px = "DIRECT", p.direct
	}
	log.Infof("[Router] UDP dial attempt=%d route=%s dstIP=%s dest=%s proto=%s stats={%s}", attempt, route, metadata.DstIP, dest, metadata.Network, p.flowStats())
	return p.dialUDPRoute(metadata, route, px, attempt, dest, start)
}

func (p *DobbyProxy) dialUDPRoute(metadata *M.Metadata, route string, px proxy.Proxy, attempt uint64, dest string, start time.Time) (net.PacketConn, error) {
	active, release, err := p.udpSlot.reserve(dest, &p.activeUDP)
	if err != nil {
		p.udpDialErr.Add(1)
		p.udpLimitErr.Add(1)
		log.Infof("[Router] %s UDP dial error attempt=%d dest=%s elapsed=%s stats={%s} err=%v", route, attempt, dest, time.Since(start), p.flowStats(), err)
		return nil, err
	}
	conn, err := px.DialUDP(metadata)
	if err != nil {
		release()
		p.udpDialErr.Add(1)
		log.Infof("[Router] %s UDP dial error attempt=%d dest=%s elapsed=%s stats={%s} err=%v", route, attempt, dest, time.Since(start), p.flowStats(), err)
		return nil, err
	}
	p.udpDialOK.Add(1)
	updatePeakInt64(&p.peakUDP, active)
	log.Infof("[Router] %s UDP dial OK attempt=%d dest=%s elapsed=%s local=%s stats={%s}", route, attempt, dest, time.Since(start), conn.LocalAddr(), p.flowStats())
	tracked := &trackedPacketConn{PacketConn: conn, release: release, route: route, dest: dest, started: time.Now()}
	return newIdlePacketConn(tracked, udpAssociationIdleTimeout, route, dest, func() uint64 {
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

	wrapper := &DobbyProxy{
		vpn:    vpnOutbound,
		direct: &protected_dialer.ProtectedDirectProxy{Proxy: proxy.NewDirect()},
		tcpSlot: flowSlot{
			byHost:   make(map[string]int),
			byDest:   make(map[string]int),
			maxTotal: maxActiveTCPConnections,
			maxHost:  maxActiveTCPConnectionsPerHost,
			maxDest:  maxActiveTCPConnectionsPerDest,
		},
		udpSlot: flowSlot{
			byHost:   make(map[string]int),
			byDest:   make(map[string]int),
			maxTotal: maxActiveUDPAssociations,
			maxHost:  maxActiveUDPAssociationsPerHost,
			maxDest:  maxActiveUDPAssociationsPerDest,
		},
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

type flowSlot struct {
	mu       sync.Mutex
	byHost   map[string]int
	byDest   map[string]int
	maxTotal int64
	maxHost  int
	maxDest  int
}

func (s *flowSlot) reserve(dest string, active *atomic.Int64) (int64, func() int64, error) {
	host := flowHost(dest)

	s.mu.Lock()
	defer s.mu.Unlock()

	cur := active.Load()
	switch {
	case cur >= s.maxTotal:
		return cur, nil, fmt.Errorf("flow limit reached active=%d max=%d", cur, s.maxTotal)
	case s.byHost[host] >= s.maxHost:
		return cur, nil, fmt.Errorf("host flow limit reached host=%s active=%d max=%d", host, s.byHost[host], s.maxHost)
	case s.byDest[dest] >= s.maxDest:
		return cur, nil, fmt.Errorf("destination flow limit reached dest=%s active=%d max=%d", dest, s.byDest[dest], s.maxDest)
	}

	s.byHost[host]++
	s.byDest[dest]++
	cur = active.Add(1)

	release := func() int64 {
		s.mu.Lock()
		defer s.mu.Unlock()
		decrementFlowCount(s.byHost, host)
		decrementFlowCount(s.byDest, dest)
		return active.Add(-1)
	}

	return cur, release, nil
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
