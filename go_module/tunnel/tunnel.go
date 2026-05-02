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

const (
	maxActiveTCPConns = 150
	maxActiveUDPConns = 150
	udpIdleTimeout    = 15 * time.Second
)

var (
	mu        sync.Mutex
	isRunning bool
)

type trackedConn struct {
	net.Conn
	counter *atomic.Int64
	once    sync.Once
}

func (c *trackedConn) Close() error {
	c.once.Do(func() {
		active := c.counter.Add(-1)
		if active%10 == 0 {
			log.Infof("[Router] TCP closed: activeTCP=%d/%d", active, maxActiveTCPConns)
		}
	})
	return c.Conn.Close()
}

type trackedPacketConn struct {
	net.PacketConn
	counter      *atomic.Int64
	once         sync.Once
	lastActivity atomic.Int64
	done         chan struct{}
}

func newTrackedPacketConn(conn net.PacketConn, counter *atomic.Int64) *trackedPacketConn {
	c := &trackedPacketConn{
		PacketConn: conn,
		counter:    counter,
		done:       make(chan struct{}),
	}
	c.lastActivity.Store(time.Now().UnixNano())
	go func() {
		ticker := time.NewTicker(udpIdleTimeout)
		defer ticker.Stop()
		for {
			select {
			case <-c.done:
				return
			case <-ticker.C:
				if time.Since(time.Unix(0, c.lastActivity.Load())) >= udpIdleTimeout {
					c.Close()
					return
				}
			}
		}
	}()
	return c
}

func (c *trackedPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, addr, err := c.PacketConn.ReadFrom(b)
	if err == nil {
		c.lastActivity.Store(time.Now().UnixNano())
	}
	return n, addr, err
}

func (c *trackedPacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	c.lastActivity.Store(time.Now().UnixNano())
	return c.PacketConn.WriteTo(b, addr)
}

func (c *trackedPacketConn) Close() error {
	var err error
	c.once.Do(func() {
		close(c.done)
		active := c.counter.Add(-1)
		log.Infof("[Router] UDP closed (idle): activeUDP=%d/%d", active, maxActiveUDPConns)
		err = c.PacketConn.Close()
	})
	return err
}

type DobbyProxy struct {
	vpn       proxy.Proxy
	direct    proxy.Proxy
	activeTCP atomic.Int64
	activeUDP atomic.Int64
}

func (p *DobbyProxy) DialContext(ctx context.Context, metadata *M.Metadata) (net.Conn, error) {
	if IsBypass(metadata) {
		conn, err := p.direct.DialContext(ctx, metadata)
		if err != nil {
			log.Infof("[Router] BYPASS TCP dial error %s: %v", metadata.DestinationAddress(), err)
			return nil, err
		}
		log.Infof("[DEBUG][Router] BYPASS TCP dial OK %s local=%s remote=%s", metadata.DestinationAddress(), conn.LocalAddr(), conn.RemoteAddr())
		return conn, nil
	}

	// Increment first, then check — prevents TOCTOU race where multiple goroutines
	// all pass the Load() check before any of them reaches Add(1).
	active := p.activeTCP.Add(1)
	if active > maxActiveTCPConns {
		p.activeTCP.Add(-1)
		log.Infof("[Router] TCP dropped (activeTCP=%d): %s", active-1, metadata.DestinationAddress())
		return nil, fmt.Errorf("too many active TCP connections")
	}

	conn, err := p.vpn.DialContext(ctx, metadata)
	if err != nil {
		p.activeTCP.Add(-1)
		log.Infof("[Router] PROXY TCP dial error %s: %v", metadata.DestinationAddress(), err)
		return nil, err
	}

	if active%10 == 0 {
		log.Infof("[Router] pool: activeTCP=%d/%d activeUDP=%d/%d", active, maxActiveTCPConns, p.activeUDP.Load(), maxActiveUDPConns)
	}
	return &trackedConn{Conn: conn, counter: &p.activeTCP}, nil
}

func (p *DobbyProxy) DialUDP(metadata *M.Metadata) (net.PacketConn, error) {
	if IsBypass(metadata) {
		conn, err := p.direct.DialUDP(metadata)
		if err != nil {
			log.Infof("[Router] BYPASS UDP dial error %s: %v", metadata.DestinationAddress(), err)
			return nil, err
		}
		log.Infof("[DEBUG][Router] BYPASS UDP dial OK %s local=%s", metadata.DestinationAddress(), conn.LocalAddr())
		return conn, nil
	}

	active := p.activeUDP.Add(1)
	if active > maxActiveUDPConns {
		p.activeUDP.Add(-1)
		log.Infof("[Router] UDP dropped (activeUDP=%d): %s", active-1, metadata.DestinationAddress())
		return nil, fmt.Errorf("too many active UDP connections")
	}

	conn, err := p.vpn.DialUDP(metadata)
	if err != nil {
		p.activeUDP.Add(-1)
		log.Infof("[Router] PROXY UDP dial error %s: %v", metadata.DestinationAddress(), err)
		return nil, err
	}

	if active%10 == 0 {
		log.Infof("[Router] pool: activeTCP=%d/%d activeUDP=%d/%d", p.activeTCP.Load(), maxActiveTCPConns, active, maxActiveUDPConns)
	}
	return newTrackedPacketConn(conn, &p.activeUDP), nil
}

func (p *DobbyProxy) Addr() string {
	return p.vpn.Addr()
}

func (p *DobbyProxy) Proto() proto.Proto {
	return p.vpn.Proto()
}

func stopLocked() {
	log.Infof("[Engine] stopping tun2socks engine")
	platform_engine.EngineStop()
	isRunning = false
	log.Infof("[Engine] tun2socks engine stopped")
}

func StartEngine(cfg platform_engine.EngineConfig) error {
	mu.Lock()
	defer mu.Unlock()

	if isRunning {
		log.Infof("[Engine] StartEngine requested while already running; stopping previous engine first")
		stopLocked()
	}

	log.Infof("[Engine] StartEngine config proxy=%s fd=%d mtu=%d uplinkIface=%s", cfg.ProxyAddr, cfg.FD, cfg.MTU, cfg.UplinkIface)
	log.Infof("[Engine] StartEngine: calling StartPlatformEngine")
	err := platform_engine.StartPlatformEngine(cfg)
	if err != nil {
		log.Infof("[Engine] StartPlatformEngine failed: %v", err)
		return err
	}
	log.Infof("[Engine] StartPlatformEngine OK")

	t := tunnel.T()
	if t == nil {
		log.Infof("[Engine] tunnel.T() is nil after engine start — tun2socks did not initialise")
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
	log.Infof("[Engine] DobbyProxy installed (maxTCP=%d maxUDP=%d)", maxActiveTCPConns, maxActiveUDPConns)
	isRunning = true
	return nil
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
