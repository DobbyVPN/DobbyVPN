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
)

type DobbyProxy struct {
	vpn       proxy.Proxy
	direct    proxy.Proxy
	activeTCP atomic.Int64
	activeUDP atomic.Int64
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
	if IsBypass(metadata) {
		log.Infof("[Router] TCP dial attempt route=DIRECT dstIP=%s dest=%s proto=%s activeTCP=%d activeUDP=%d", metadata.DstIP, dest, metadata.Network, p.activeTCP.Load(), p.activeUDP.Load())
		conn, err := p.direct.DialContext(ctx, metadata)
		if err != nil {
			log.Infof("[Router] DIRECT TCP dial error dest=%s elapsed=%s err=%v", dest, time.Since(start), err)
			return nil, err
		}
		active := p.activeTCP.Add(1)
		log.Infof("[Router] DIRECT TCP dial OK dest=%s elapsed=%s local=%s remote=%s activeTCP=%d", dest, time.Since(start), conn.LocalAddr(), conn.RemoteAddr(), active)
		return &trackedConn{Conn: conn, counter: &p.activeTCP, route: "DIRECT", dest: dest, started: time.Now()}, nil
	}
	log.Infof("[Router] TCP dial attempt route=VPN dstIP=%s dest=%s proto=%s activeTCP=%d activeUDP=%d", metadata.DstIP, dest, metadata.Network, p.activeTCP.Load(), p.activeUDP.Load())
	conn, err := p.vpn.DialContext(ctx, metadata)
	if err != nil {
		log.Infof("[Router] VPN TCP dial error dest=%s elapsed=%s err=%v", dest, time.Since(start), err)
		return nil, err
	}
	active := p.activeTCP.Add(1)
	log.Infof("[Router] VPN TCP dial OK dest=%s elapsed=%s local=%s remote=%s activeTCP=%d", dest, time.Since(start), conn.LocalAddr(), conn.RemoteAddr(), active)
	return &trackedConn{Conn: conn, counter: &p.activeTCP, route: "VPN", dest: dest, started: time.Now()}, nil
}

func (p *DobbyProxy) DialUDP(metadata *M.Metadata) (net.PacketConn, error) {
	start := time.Now()
	dest := metadata.DestinationAddress()
	if IsBypass(metadata) {
		log.Infof("[Router] UDP dial attempt route=DIRECT dstIP=%s dest=%s proto=%s activeTCP=%d activeUDP=%d", metadata.DstIP, dest, metadata.Network, p.activeTCP.Load(), p.activeUDP.Load())
		conn, err := p.direct.DialUDP(metadata)
		if err != nil {
			log.Infof("[Router] DIRECT UDP dial error dest=%s elapsed=%s err=%v", dest, time.Since(start), err)
			return nil, err
		}
		active := p.activeUDP.Add(1)
		log.Infof("[Router] DIRECT UDP dial OK dest=%s elapsed=%s local=%s activeUDP=%d", dest, time.Since(start), conn.LocalAddr(), active)
		return &trackedPacketConn{PacketConn: conn, counter: &p.activeUDP, route: "DIRECT", dest: dest, started: time.Now()}, nil
	}
	log.Infof("[Router] UDP dial attempt route=VPN dstIP=%s dest=%s proto=%s activeTCP=%d activeUDP=%d", metadata.DstIP, dest, metadata.Network, p.activeTCP.Load(), p.activeUDP.Load())
	conn, err := p.vpn.DialUDP(metadata)
	if err != nil {
		log.Infof("[Router] VPN UDP dial error dest=%s elapsed=%s err=%v", dest, time.Since(start), err)
		return nil, err
	}
	active := p.activeUDP.Add(1)
	log.Infof("[Router] VPN UDP dial OK dest=%s elapsed=%s local=%s activeUDP=%d", dest, time.Since(start), conn.LocalAddr(), active)
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
	isRunning = true
	return nil
}

func stopLocked() {
	log.Infof("[Engine] stopping tun2socks engine")
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
