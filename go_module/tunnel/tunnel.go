package tunnel

import (
	"context"
	"fmt"
	"net"
	"sync"
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
	maxConcurrentVPNDials = 30
	dialQueueTimeout      = 20 * time.Second
)

var (
	mu        sync.Mutex
	isRunning bool
)

type DobbyProxy struct {
	vpn    proxy.Proxy
	direct proxy.Proxy
	tcpSem chan struct{}
}

func (p *DobbyProxy) DialContext(ctx context.Context, metadata *M.Metadata) (net.Conn, error) {
	if IsBypass(metadata) {
		return p.direct.DialContext(ctx, metadata)
	}

	timer := time.NewTimer(dialQueueTimeout)
	defer timer.Stop()
	select {
	case p.tcpSem <- struct{}{}:
		defer func() { <-p.tcpSem }()
	case <-timer.C:
		log.Infof("[Router] TCP dropped (queue full): %s", metadata.DestinationAddress())
		return nil, fmt.Errorf("dial queue timeout for %s", metadata.DestinationAddress())
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return p.vpn.DialContext(ctx, metadata)
}

func (p *DobbyProxy) DialUDP(metadata *M.Metadata) (net.PacketConn, error) {
	if IsBypass(metadata) {
		return p.direct.DialUDP(metadata)
	}
	return p.vpn.DialUDP(metadata)
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
		tcpSem: make(chan struct{}, maxConcurrentVPNDials),
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
