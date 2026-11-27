package dnscache_test

import (
	"context"
	"net"
	"net/netip"
	"testing"

	"core/dnscache"
	"core/iprange"
	"core/tunnel"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"
)

// routerDNS answers the first query of each name and then stops answering,
// like a router whose DNS hangs after the connection was set up (#507).
func routerDNS(address string) dnscache.Resolver {
	answered := map[string]bool{}
	return func(_ context.Context, host string) ([]net.IPAddr, error) {
		if answered[host] {
			return nil, context.DeadlineExceeded
		}
		answered[host] = true
		return []net.IPAddr{{IP: net.ParseIP(address)}}, nil
	}
}

// When the router's DNS stops answering, the VPN server endpoint is still
// reached through the address remembered from the last answer.
func TestServerEndpointUsesRememberedAddressWhenRouterDNSStops(t *testing.T) {
	cache := dnscache.New()
	t.Cleanup(cache.Close)
	cache.SetResolver("home-wifi", routerDNS("192.0.2.10"))
	if _, err := cache.ResolveServerAddresses(context.Background(), "vpn.example"); err != nil {
		t.Fatal(err)
	}
	addresses, err := cache.ResolveServerAddresses(context.Background(), "vpn.example")
	if err != nil || len(addresses) != 1 || addresses[0].IP.String() != "192.0.2.10" {
		t.Fatalf("the endpoint must use the remembered address: %v err=%v", addresses, err)
	}
}

// Any other name (an Xray direct destination, the subscription address, an
// ExcludeIPs host name) fails as before when the router's DNS stops: its
// earlier answer was never remembered.
func TestOtherNameFailsWhenRouterDNSStops(t *testing.T) {
	cache := dnscache.New()
	t.Cleanup(cache.Close)
	cache.SetResolver("home-wifi", routerDNS("192.0.2.20"))
	if _, err := cache.ResolveAddresses(context.Background(), "site.example"); err != nil {
		t.Fatal(err)
	}
	if addresses, err := cache.ResolveAddresses(context.Background(), "site.example"); err == nil {
		t.Fatalf("a name other than the server must not get a remembered answer: %v", addresses)
	}
}

// Even the server's own name gets no remembered answer outside the server
// endpoint path, e.g. when it is also listed in ExcludeIPs.
func TestServerNameOutsideEndpointPathGetsNoRememberedAnswer(t *testing.T) {
	cache := dnscache.New()
	t.Cleanup(cache.Close)
	cache.SetResolver("home-wifi", routerDNS("192.0.2.10"))
	if _, err := cache.ResolveServerAddresses(context.Background(), "vpn.example"); err != nil {
		t.Fatal(err)
	}
	if addresses, err := cache.ResolveAddresses(context.Background(), "vpn.example"); err == nil {
		t.Fatalf("ResolveAddresses must not serve the remembered answer: %v", addresses)
	}
}

// An ExcludeIPs host name with a stopped router DNS is ignored: its traffic
// stays in the VPN, as before #507, and no old address is routed outside.
func TestExcludedHostWithStoppedRouterDNSStaysInTheVPN(t *testing.T) {
	cache := dnscache.New()
	t.Cleanup(cache.Close)
	cache.SetResolver("home-wifi", routerDNS("192.0.2.20"))
	// The name was resolved once while DNS worked.
	if _, err := tunnel.ResolveBypassPolicyWithResolver(context.Background(), iprange.New(nil), []string{"nas.example"}, cache.ResolveAddresses); err != nil {
		t.Fatal(err)
	}
	policy, err := tunnel.ResolveBypassPolicyWithResolver(context.Background(), iprange.New(nil), []string{"nas.example"}, cache.ResolveAddresses)
	if err != nil {
		t.Fatal(err)
	}
	old := &M.Metadata{Network: M.TCP, DstIP: netip.MustParseAddr("192.0.2.20"), DstPort: 445}
	if policy.IsBypass(old) {
		t.Fatal("the old address must not be routed outside the VPN")
	}
}

// While DNS works, an ExcludeIPs host resolves normally and bypasses the VPN.
func TestExcludedHostWithWorkingDNSBypassesTheVPN(t *testing.T) {
	cache := dnscache.New()
	t.Cleanup(cache.Close)
	cache.SetResolver("home-wifi", func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("192.0.2.30")}}, nil
	})
	policy, err := tunnel.ResolveBypassPolicyWithResolver(context.Background(), iprange.New(nil), []string{"nas.example"}, cache.ResolveAddresses)
	if err != nil {
		t.Fatal(err)
	}
	if !policy.IsBypass(&M.Metadata{Network: M.TCP, DstIP: netip.MustParseAddr("192.0.2.30"), DstPort: 445}) {
		t.Fatal("a resolved ExcludeIPs host must bypass the VPN")
	}
}
