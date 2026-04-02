package tunnel

import (
	"context"
	"fmt"
	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	"github.com/xjasonlyu/tun2socks/v2/proxy"
	"github.com/xjasonlyu/tun2socks/v2/proxy/proto"
	"github.com/xjasonlyu/tun2socks/v2/tunnel"
	"go_client/log"
	"go_client/tunnel/platform_engine"
	"go_client/tunnel/protected_dialer"
	"net"
	"sync"
)

var (
	mu        sync.Mutex
	isRunning bool
)

type DobbyProxy struct {
	vpn    proxy.Proxy
	direct proxy.Proxy
}

func (p *DobbyProxy) DialContext(ctx context.Context, metadata *M.Metadata) (proxyConn net.Conn, err error) {
	if IsBypass(metadata) {
		log.Infof("[Router] Using DIRECT for %s", metadata.DstIP)
		return p.direct.DialContext(ctx, metadata)
	}
	log.Infof("[Router] Using VPN for %s", metadata.DstIP)
	return p.vpn.DialContext(ctx, metadata)
}

func (p *DobbyProxy) DialUDP(metadata *M.Metadata) (net.PacketConn, error) {
	if IsBypass(metadata) {
		log.Infof("[Router] Using UDP DIRECT for %s", metadata.DstIP)
		return p.direct.DialUDP(metadata)
	}
	log.Infof("[Router] Using UDP VPN for %s", metadata.DstIP)
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

	currentDialer := tunnel.T().Dialer()
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

	if tunnel.T() == nil {
		log.Infof("[Engine] tunnel.T() return nil")
	}

	tunnel.T().SetDialer(wrapper)
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
