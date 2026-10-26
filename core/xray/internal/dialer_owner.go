package internal

import (
	"context"
	"syscall"

	"core/dnscache"
	"core/tunnel/protected_dialer"

	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/dns"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/transport/internet"
)

// The protocol owner serializes construction and retirement of these global
// Xray hooks. Retirement happens only after all instance resources have closed.
func InstallProtectedSystemDialer(ctx context.Context, cache *dnscache.Cache) func(context.Context) error {
	dialer := newProtectedSystemDialer(func(network, address string, connection syscall.RawConn) error {
		return protected_dialer.ProtectRawConnContext(ctx, network, address, connection)
	}, applyPlatformOutboundSocketOptions)
	dialer.resolver, dialer.owner = cache, ctx
	internet.UseAlternativeSystemDialer(dialer)
	return func(cleanup context.Context) error {
		if err := dialer.sockets.close(cleanup); err != nil {
			return err
		}
		internet.UseAlternativeSystemDialer(nil)
		internet.InitSystemDialer(nil, nil)
		return nil
	}
}

func BindUnderlyingDNS(ctx context.Context, instance *core.Instance, cache *dnscache.Cache) {
	manager, _ := instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
	internet.InitSystemDialer(&underlyingDNS{cache: cache, ctx: ctx}, manager)
}

type underlyingDNS struct {
	cache *dnscache.Cache
	ctx   context.Context
}

func (*underlyingDNS) Type() interface{} { return dns.ClientType() }
func (*underlyingDNS) Start() error      { return nil }
func (*underlyingDNS) Close() error      { return nil }
func (d *underlyingDNS) LookupIP(host string, options dns.IPOption) (ips []xnet.IP, ttl uint32, lookupErr error) {
	ctx, cancel := context.WithTimeout(d.ctx, dnscache.ServerResolveTimeout)
	defer cancel()
	addresses, err := d.cache.ResolveAddresses(ctx, host)
	if err != nil {
		return nil, 0, err
	}
	ips = make([]xnet.IP, 0, len(addresses))
	for _, address := range addresses {
		if address.IP.To4() != nil && options.IPv4Enable || address.IP.To4() == nil && options.IPv6Enable {
			ips = append(ips, address.IP)
		}
	}
	if len(ips) == 0 {
		return nil, 0, dns.ErrEmptyResponse
	}
	// Native cache owns validity; no second Xray TTL survives network changes.
	return ips, 0, nil
}
