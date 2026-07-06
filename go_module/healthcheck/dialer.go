package healthcheck

import (
	"context"
	"net"
	"time"

	"go_module/dnscache"
)

func cachedDialContext(timeout time.Duration, source string) func(context.Context, string, string) (net.Conn, error) {
	base := &net.Dialer{Timeout: timeout, KeepAlive: -1}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return base.DialContext(ctx, network, addr)
		}
		ip, err := dnscache.ResolveIPv4(ctx, host, timeout, source)
		if err != nil {
			return nil, err
		}
		return base.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
	}
}
