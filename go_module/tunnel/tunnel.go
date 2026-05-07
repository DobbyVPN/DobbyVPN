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

type DobbyProxy struct {
	vpn       proxy.Proxy
	direct    proxy.Proxy
	activeTCP atomic.Int64
	activeUDP atomic.Int64
	peakTCP   atomic.Int64
	peakUDP   atomic.Int64

	tcpDialAttempt atomic.Uint64
	tcpDialOK      atomic.Uint64
	tcpDialErr     atomic.Uint64
	udpDialAttempt atomic.Uint64
	udpDialOK      atomic.Uint64
	udpDialErr     atomic.Uint64
}

type trackedConn struct {
	net.Conn
	counter *atomic.Int64
	route   string
	dest    string
	started time.Time
	once    sync.Once
}

func (c *trackedConn) Close() error {
	var err error
	c.once.Do(func() {
		active := c.counter.Add(-1)
		log.Infof("[Router] TCP closed route=%s dest=%s lifetime=%s activeTCP=%d", c.route, c.dest, time.Since(c.started), active)
		err = c.Conn.Close()
	})
	return err
}

type trackedPacketConn struct {
	net.PacketConn
	counter *atomic.Int64
	route   string
	dest    string
	started time.Time
	once    sync.Once
}

func (c *trackedPacketConn) Close() error {
	var err error
	c.once.Do(func() {
		active := c.counter.Add(-1)
		log.Infof("[Router] UDP closed route=%s dest=%s lifetime=%s activeUDP=%d", c.route, c.dest, time.Since(c.started), active)
		err = c.PacketConn.Close()
	})
	return err
}

func (p *DobbyProxy) DialContext(ctx context.Context, metadata *M.Metadata) (net.Conn, error) {
	start := time.Now()
	dest := metadata.DestinationAddress()
	attempt := p.tcpDialAttempt.Add(1)
	if IsBypass(metadata) {
		log.Infof("[Router] TCP dial attempt=%d route=DIRECT dstIP=%s dest=%s proto=%s stats={%s}", attempt, metadata.DstIP, dest, metadata.Network, p.flowStats())
		conn, err := p.direct.DialContext(ctx, metadata)
		if err != nil {
			p.tcpDialErr.Add(1)
			log.Infof("[Router] DIRECT TCP dial error attempt=%d dest=%s elapsed=%s stats={%s} err=%v", attempt, dest, time.Since(start), p.flowStats(), err)
			return nil, err
		}
		p.tcpDialOK.Add(1)
		active := p.activeTCP.Add(1)
		updatePeakInt64(&p.peakTCP, active)
		log.Infof("[Router] DIRECT TCP dial OK attempt=%d dest=%s elapsed=%s local=%s remote=%s stats={%s}", attempt, dest, time.Since(start), conn.LocalAddr(), conn.RemoteAddr(), p.flowStats())
		return &trackedConn{Conn: conn, counter: &p.activeTCP, route: "DIRECT", dest: dest, started: time.Now()}, nil
	}
	log.Infof("[Router] TCP dial attempt=%d route=VPN dstIP=%s dest=%s proto=%s stats={%s}", attempt, metadata.DstIP, dest, metadata.Network, p.flowStats())
	conn, err := p.vpn.DialContext(ctx, metadata)
	if err != nil {
		p.tcpDialErr.Add(1)
		log.Infof("[Router] VPN TCP dial error attempt=%d dest=%s elapsed=%s stats={%s} err=%v", attempt, dest, time.Since(start), p.flowStats(), err)
		return nil, err
	}
	p.tcpDialOK.Add(1)
	active := p.activeTCP.Add(1)
	updatePeakInt64(&p.peakTCP, active)
	log.Infof("[Router] VPN TCP dial OK attempt=%d dest=%s elapsed=%s local=%s remote=%s stats={%s}", attempt, dest, time.Since(start), conn.LocalAddr(), conn.RemoteAddr(), p.flowStats())
	return &trackedConn{Conn: conn, counter: &p.activeTCP, route: "VPN", dest: dest, started: time.Now()}, nil
}

func (p *DobbyProxy) DialUDP(metadata *M.Metadata) (net.PacketConn, error) {
	start := time.Now()
	dest := metadata.DestinationAddress()
	attempt := p.udpDialAttempt.Add(1)
	if IsBypass(metadata) {
		log.Infof("[Router] UDP dial attempt=%d route=DIRECT dstIP=%s dest=%s proto=%s stats={%s}", attempt, metadata.DstIP, dest, metadata.Network, p.flowStats())
		conn, err := p.direct.DialUDP(metadata)
		if err != nil {
			p.udpDialErr.Add(1)
			log.Infof("[Router] DIRECT UDP dial error attempt=%d dest=%s elapsed=%s stats={%s} err=%v", attempt, dest, time.Since(start), p.flowStats(), err)
			return nil, err
		}
		p.udpDialOK.Add(1)
		active := p.activeUDP.Add(1)
		updatePeakInt64(&p.peakUDP, active)
		log.Infof("[Router] DIRECT UDP dial OK attempt=%d dest=%s elapsed=%s local=%s stats={%s}", attempt, dest, time.Since(start), conn.LocalAddr(), p.flowStats())
		return &trackedPacketConn{PacketConn: conn, counter: &p.activeUDP, route: "DIRECT", dest: dest, started: time.Now()}, nil
	}
	log.Infof("[Router] UDP dial attempt=%d route=VPN dstIP=%s dest=%s proto=%s stats={%s}", attempt, metadata.DstIP, dest, metadata.Network, p.flowStats())
	conn, err := p.vpn.DialUDP(metadata)
	if err != nil {
		p.udpDialErr.Add(1)
		log.Infof("[Router] VPN UDP dial error attempt=%d dest=%s elapsed=%s stats={%s} err=%v", attempt, dest, time.Since(start), p.flowStats(), err)
		return nil, err
	}
	p.udpDialOK.Add(1)
	active := p.activeUDP.Add(1)
	updatePeakInt64(&p.peakUDP, active)
	log.Infof("[Router] VPN UDP dial OK attempt=%d dest=%s elapsed=%s local=%s stats={%s}", attempt, dest, time.Since(start), conn.LocalAddr(), p.flowStats())
	return &trackedPacketConn{PacketConn: conn, counter: &p.activeUDP, route: "VPN", dest: dest, started: time.Now()}, nil
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
		vpn:    vpnOutbound,
		direct: directOutbound,
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
		"activeTCP=%d peakTCP=%d activeUDP=%d peakUDP=%d tcp=%d/%d/%d udp=%d/%d/%d",
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
	)
}

func (p *DobbyProxy) runtimeStats() string {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	return fmt.Sprintf(
		"goroutines=%d allocMB=%.2f totalAllocMB=%.2f sysMB=%.2f heapAllocMB=%.2f heapInuseMB=%.2f heapIdleMB=%.2f stackInuseMB=%.2f nextGCMB=%.2f numGC=%d",
		runtime.NumGoroutine(),
		bytesToMB(mem.Alloc),
		bytesToMB(mem.TotalAlloc),
		bytesToMB(mem.Sys),
		bytesToMB(mem.HeapAlloc),
		bytesToMB(mem.HeapInuse),
		bytesToMB(mem.HeapIdle),
		bytesToMB(mem.StackInuse),
		bytesToMB(mem.NextGC),
		mem.NumGC,
	)
}

func (p *DobbyProxy) logStatsLoop(stop <-chan struct{}) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			log.Infof("[Router STATS] flow={%s} runtime={%s}", p.flowStats(), p.runtimeStats())
		case <-stop:
			log.Infof("[Router STATS] stopped flow={%s} runtime={%s}", p.flowStats(), p.runtimeStats())
			return
		}
	}
}

func bytesToMB(v uint64) float64 {
	return float64(v) / 1024.0 / 1024.0
}

func updatePeakInt64(peak *atomic.Int64, current int64) {
	for {
		old := peak.Load()
		if current <= old || peak.CompareAndSwap(old, current) {
			return
		}
	}
}
