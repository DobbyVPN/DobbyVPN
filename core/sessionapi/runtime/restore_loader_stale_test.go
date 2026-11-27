//go:build (linux && !android) || windows

package runtime

import (
	"context"
	"net"
	"testing"

	"core/dnscache"
)

// When the router's DNS stops, the subscription fetch does not dial a
// remembered address, even for the server's own name: it fails as before.
func TestSubscriptionFetchGetsNoRememberedAddressWhenDNSStops(t *testing.T) {
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
	if _, err := cache.ResolveServerAddresses(context.Background(), "vpn.example"); err != nil {
		t.Fatal(err)
	}
	dial := func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("the subscription fetch dialed a remembered address")
		return nil, nil
	}
	if _, err := dialRestoreOrigin(context.Background(), "tcp", "vpn.example:443", cache, dial); err == nil {
		t.Fatal("the subscription fetch must fail while DNS does not answer")
	}
}
