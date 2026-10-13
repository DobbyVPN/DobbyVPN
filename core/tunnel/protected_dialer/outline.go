package protected_dialer

import (
	"context"
	"fmt"
	"net"

	"golang.getoutline.org/sdk/transport"
	"golang.getoutline.org/sdk/x/configurl"

	"core/dnscache"
	"core/log"
)

type outlineStreamDialer struct {
	dnsCache *dnscache.Cache
	register func(context.Context, net.Conn) error
}

func (d outlineStreamDialer) DialStream(ctx context.Context, addr string) (transport.StreamConn, error) {
	conn, err := DialContextWithProtect(ctx, d.dnsCache, "tcp", addr)
	if err != nil {
		return nil, err
	}
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("outline protected stream dialer returned %T for %s", conn, addr)
	}
	if d.register != nil {
		if err := d.register(ctx, tcpConn); err != nil {
			_ = tcpConn.Close()
			return nil, fmt.Errorf("register pending Outline stream connection: %w", err)
		}
	}
	return tcpConn, nil
}

type outlinePacketDialer struct{ dnsCache *dnscache.Cache }

func (d outlinePacketDialer) DialPacket(ctx context.Context, addr string) (net.Conn, error) {
	return DialUDPConnWithProtect(ctx, d.dnsCache, "udp", addr)
}

func NewOutlineProviders(dnsCache *dnscache.Cache, register func(context.Context, net.Conn) error) *configurl.ProviderContainer {
	providers := &configurl.ProviderContainer{
		StreamDialers: configurl.NewExtensibleProvider[transport.StreamDialer](outlineStreamDialer{
			dnsCache: dnsCache,
			register: register,
		}),
		PacketDialers:   configurl.NewExtensibleProvider[transport.PacketDialer](outlinePacketDialer{dnsCache}),
		PacketListeners: configurl.NewExtensibleProvider[transport.PacketListener](&transport.UDPListener{}),
	}
	log.Debugf(Category, "[Protect][Outline] SDK providers use attempt-scoped protected stream/packet dialers")
	return configurl.RegisterDefaultProviders(providers)
}
