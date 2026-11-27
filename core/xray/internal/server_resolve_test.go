package internal

import (
	"context"
	"net"
	"testing"

	"core/dnscache"

	"github.com/xtls/xray-core/common/session"
)

// stoppedRouterCache remembers the server's answer, then its DNS stops.
func stoppedRouterCache(t *testing.T, host string) *dnscache.Cache {
	t.Helper()
	cache := dnscache.New()
	t.Cleanup(cache.Close)
	answered := false
	cache.SetResolver("home-wifi", func(context.Context, string) ([]net.IPAddr, error) {
		if answered {
			return nil, context.DeadlineExceeded
		}
		answered = true
		return []net.IPAddr{{IP: net.ParseIP("192.0.2.10")}}, nil
	})
	if _, err := cache.ResolveServerAddresses(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	return cache
}

func outboundContext(name string) context.Context {
	return session.ContextWithOutbounds(context.Background(), []*session.Outbound{{Name: name}})
}

// When the router's DNS stops, Xray's proxy outbound still reaches the
// server's remembered address, but a direct (freedom) outbound to the same
// name, or a dial whose outbound is unknown, does not get it.
func TestXrayOnlyServerOutboundUsesRememberedAddress(t *testing.T) {
	cache := stoppedRouterCache(t, "vpn.example")
	dialer := &protectedSystemDialer{resolver: cache}
	addresses, err := dialer.resolveDestination(outboundContext("vless"), "vpn.example")
	if err != nil || len(addresses) != 1 || addresses[0].IP.String() != "192.0.2.10" {
		t.Fatalf("the proxy outbound must reach the remembered server address: %v err=%v", addresses, err)
	}
	for name, ctx := range map[string]context.Context{"freedom": outboundContext("freedom"), "unknown": context.Background()} {
		if addresses, err := dialer.resolveDestination(ctx, "vpn.example"); err == nil {
			t.Fatalf("%s outbound got a remembered address %v", name, addresses)
		}
	}
}
