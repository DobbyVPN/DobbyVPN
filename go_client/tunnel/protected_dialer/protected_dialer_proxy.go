package protected_dialer

import (
	"context"
	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	"github.com/xjasonlyu/tun2socks/v2/proxy"
	"net"
)

type ProtectedDirectProxy struct {
	proxy.Proxy
}

type ProtectDialer func(ctx context.Context, network, address string) (net.Conn, error)
type ProtectPacketDialer func(ctx context.Context, network, address string) (net.PacketConn, error)

func (p *ProtectedDirectProxy) DialContext(ctx context.Context, metadata *M.Metadata) (net.Conn, error) {
	network := metadata.Network.String()
	address := metadata.DestinationAddress()

	return DialContextWithProtect(ctx, network, address)
}

func (p *ProtectedDirectProxy) DialUDP(metadata *M.Metadata) (net.PacketConn, error) {
	network := metadata.Network.String()
	address := metadata.DestinationAddress()

	return DialUDPWithProtect(context.Background(), network, address)
}
