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
	owner    context.Context
	dnsCache *dnscache.Cache
	register func(context.Context, net.Conn) error
}

func (d outlineStreamDialer) DialStream(ctx context.Context, addr string) (transport.StreamConn, error) {
	conn, err := DialContextWithProtect(withSocketScope(ctx, d.owner), d.dnsCache, "tcp", addr)
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

type outlinePacketDialer struct {
	dnsCache *dnscache.Cache
	owner    context.Context
}

func (d outlinePacketDialer) DialPacket(ctx context.Context, addr string) (net.Conn, error) {
	return DialUDPConnWithProtect(withSocketScope(ctx, d.owner), d.dnsCache, "udp", addr)
}

func NewOutlineProviders(owner context.Context, dnsCache *dnscache.Cache, register func(context.Context, net.Conn) error) *configurl.ProviderContainer {
	providers := &configurl.ProviderContainer{
		StreamDialers: configurl.NewExtensibleProvider[transport.StreamDialer](outlineStreamDialer{
			owner:    owner,
			dnsCache: dnsCache,
			register: register,
		}),
		PacketDialers:   configurl.NewExtensibleProvider[transport.PacketDialer](outlinePacketDialer{dnsCache, owner}),
		PacketListeners: configurl.NewExtensibleProvider[transport.PacketListener](&transport.UDPListener{}),
	}
	log.Debugf(Category, "[Protect][Outline] SDK providers use attempt-scoped protected stream/packet dialers")
	configurl.RegisterDefaultProviders(providers)
	registerOutlineWebsocketStreams(providers)
	registerOutlineWebsocketPackets(providers)
	return providers
}
