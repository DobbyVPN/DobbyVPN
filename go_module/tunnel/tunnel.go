package tunnel

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	"github.com/xjasonlyu/tun2socks/v2/proxy"
	"github.com/xjasonlyu/tun2socks/v2/proxy/proto"
	"github.com/xjasonlyu/tun2socks/v2/tunnel"

	"go_module/log"
	"go_module/tunnel/platform_engine"
	"go_module/tunnel/protected_dialer"
)

const (
	maxActiveTCPConns = 75
	maxActiveUDPConns = 75
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
	c.once.Do(func() { c.counter.Add(-1) })
	return c.Conn.Close()
}

type trackedPacketConn struct {
	net.PacketConn
	counter *atomic.Int64
	once    sync.Once
}

func (c *trackedPacketConn) Close() error {
	c.once.Do(func() { c.counter.Add(-1) })
	return c.PacketConn.Close()
}

type DobbyProxy struct {
	vpn       proxy.Proxy
	direct    proxy.Proxy
	activeTCP atomic.Int64
	activeUDP atomic.Int64
}

func (p *DobbyProxy) DialContext(ctx context.Context, metadata *M.Metadata) (net.Conn, error) {
	if IsBypass(metadata) {
		return p.direct.DialContext(ctx, metadata)
	}

	if active := p.activeTCP.Load(); active >= maxActiveTCPConns {
		log.Infof("[Router] TCP dropped (activeTCP=%d): %s", active, metadata.DestinationAddress())
		return nil, fmt.Errorf("too many active TCP connections")
	}

	conn, err := p.vpn.DialContext(ctx, metadata)
	if err != nil {
		return nil, err
	}

	p.activeTCP.Add(1)
	return &trackedConn{Conn: conn, counter: &p.activeTCP}, nil
}

func (p *DobbyProxy) DialUDP(metadata *M.Metadata) (net.PacketConn, error) {
	if IsBypass(metadata) {
		return p.direct.DialUDP(metadata)
	}

	if active := p.activeUDP.Load(); active >= maxActiveUDPConns {
		log.Infof("[Router] UDP dropped (activeUDP=%d): %s", active, metadata.DestinationAddress())
		return nil, fmt.Errorf("too many active UDP connections")
	}

	conn, err := p.vpn.DialUDP(metadata)
	if err != nil {
		return nil, err
	}

	p.activeUDP.Add(1)
	return &trackedPacketConn{PacketConn: conn, counter: &p.activeUDP}, nil
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
		StopEngine()
	}

	err := platform_engine.StartPlatformEngine(cfg)
	if err != nil {
		return err
	}

	t := tunnel.T()
	if t == nil {
		return fmt.Errorf("tunnel not initialized after engine start")
	}

	currentDialer := t.Dialer()
	vpnOutbound, ok := currentDialer.(proxy.Proxy)
	if !ok {
		log.Infof("[Engine] Current dialer is not a proxy")
		return fmt.Errorf("current dialer is not a proxy")
	}

	directOutbound := &protected_dialer.ProtectedDirectProxy{
		Proxy: proxy.NewDirect(),
	}

	wrapper := &DobbyProxy{
		vpn:    vpnOutbound,
		direct: directOutbound,
	}

	t.SetDialer(wrapper)
	isRunning = true
	return nil
}

func StopEngine() {
	mu.Lock()
	defer mu.Unlock()

	if !isRunning {
		return
	}

	platform_engine.EngineStop()
	isRunning = false
}
