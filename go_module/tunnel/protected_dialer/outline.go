package protected_dialer

import (
	"context"
	"fmt"
	"net"

	"golang.getoutline.org/sdk/transport"
	"golang.getoutline.org/sdk/x/configurl"

	"go_module/log"
)

type outlineStreamDialer struct{}

func (outlineStreamDialer) DialStream(ctx context.Context, addr string) (transport.StreamConn, error) {
	conn, err := DialContextWithProtect(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}

	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("outline protected stream dialer returned %T for %s", conn, addr)
	}

	return tcpConn, nil
}

type outlinePacketDialer struct{}

func (outlinePacketDialer) DialPacket(ctx context.Context, addr string) (net.Conn, error) {
	return DialUDPConnWithProtect(ctx, "udp", addr)
}

func NewOutlineProviders() *configurl.ProviderContainer {
	providers := &configurl.ProviderContainer{
		StreamDialers:   configurl.NewExtensibleProvider[transport.StreamDialer](outlineStreamDialer{}),
		PacketDialers:   configurl.NewExtensibleProvider[transport.PacketDialer](outlinePacketDialer{}),
		PacketListeners: configurl.NewExtensibleProvider[transport.PacketListener](&transport.UDPListener{}),
	}
	log.Debugf(Category, "[Protect][Outline] SDK providers use shared protected stream/packet dialers")
	return configurl.RegisterDefaultProviders(providers)
}
