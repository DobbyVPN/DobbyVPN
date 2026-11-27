package protected_dialer

import (
	"context"
	"net"
	"testing"

	"core/dnscache"
)

// When the router's DNS stops, only a dial marked as the VPN server endpoint
// gets the remembered address; application bypass traffic
// (ProtectedDirectProxy) and other dials fail as before.
func TestOnlyServerEndpointDialUsesRememberedAddress(t *testing.T) {
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
	if _, err := resolvedDialAddresses(WithServerEndpoint(context.Background()), cache, "vpn.example:443"); err != nil {
		t.Fatal(err)
	}
	addresses, err := resolvedDialAddresses(WithServerEndpoint(context.Background()), cache, "vpn.example:443")
	if err != nil || len(addresses) != 1 || addresses[0] != "192.0.2.10:443" {
		t.Fatalf("the server endpoint must use the remembered address: %v err=%v", addresses, err)
	}
	bypass := WithApplicationBypass(context.Background())
	if addresses, err := resolvedDialAddresses(bypass, cache, "vpn.example:443"); err == nil {
		t.Fatalf("an application bypass dial got a remembered address %v", addresses)
	}
}
