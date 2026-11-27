//go:build linux && !android

package runtime

import (
	"context"
	"core/dnscache"
	"core/routing"
	"net"
)

func openRestoreDialer(ctx context.Context) (restoreDial, func() error, error) {
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if isRestoreLoopback(host) {
			return (&net.Dialer{}).DialContext(ctx, network, address)
		}
		index, err := routing.LinuxPhysicalInterface(ownedTunName)
		if err != nil {
			return nil, restoreRouteError(network, err)
		}
		iface, err := net.InterfaceByIndex(index)
		if err != nil {
			return nil, err
		}
		cache := dnscache.New()
		cache.SetResolverSource(func(ctx context.Context) (string, dnscache.Resolver, error) {
			return dnscache.CaptureLinuxBootstrap(ctx, index, ownedRoutingTableID, routing.OpenLinuxResolverQuery)
		})
		dialer := dnscache.LinuxPhysicalDialer(iface.Name, ownedRoutingTableID)
		return dialRestoreOrigin(ctx, network, address, cache, dialer.DialContext)
	}
	return dial, func() error { return nil }, ctx.Err()
}
