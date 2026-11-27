package runtime

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"core/dnscache"
	"core/sessionapi"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"
)

// hangingRouterDNS answers normally until stop is called. After that it does
// not answer at all: every query hangs until its caller gives up, like a home
// router whose DNS stops answering for a minute.
type hangingRouterDNS struct {
	mu        sync.Mutex
	stopped   bool
	addresses map[string]string
}

func (r *hangingRouterDNS) stop() {
	r.mu.Lock()
	r.stopped = true
	r.mu.Unlock()
}

func (r *hangingRouterDNS) resolve(ctx context.Context, host string) ([]net.IPAddr, error) {
	r.mu.Lock()
	stopped, address := r.stopped, r.addresses[host]
	r.mu.Unlock()
	if stopped {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return []net.IPAddr{{IP: net.ParseIP(address)}}, nil
}

// The router's DNS stops answering for a minute -> the VPN still connects to
// its server, and an ExcludeIPs host is not routed to a remembered old address.
func TestRouterDNSStopsAnsweringForAMinute(t *testing.T) {
	const server, nas = "vpn.example", "nas.example"
	router := &hangingRouterDNS{addresses: map[string]string{server: "192.0.2.10", nas: "192.0.2.20"}}
	cache := dnscache.New()
	t.Cleanup(cache.Close)
	cache.SetResolver("home-wifi", router.resolve)
	profile := sessionapi.RuntimeProfile{ExcludeHosts: []string{nas}}

	// The VPN was connected while the router's DNS still worked, so both
	// names have a remembered answer.
	if _, err := cache.ResolveAddresses(context.Background(), server); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveBypassPolicy(context.Background(), cache, profile); err != nil {
		t.Fatal(err)
	}

	router.stop()

	t.Run("the VPN still connects to its server", func(t *testing.T) {
		started := time.Now()
		addresses, err := cache.ResolveAddresses(context.Background(), server)
		if err != nil || len(addresses) != 1 || addresses[0].IP.String() != "192.0.2.10" {
			t.Fatalf("the reconnect must reach the server's known address: %v err=%v", addresses, err)
		}
		if elapsed := time.Since(started); elapsed >= dnscache.ServerResolveTimeout {
			t.Fatalf("the reconnect waited %s for the hung DNS", elapsed)
		}
	})

	t.Run("an ExcludeIPs host is not routed to a remembered old address", func(t *testing.T) {
		policy, err := resolveBypassPolicy(context.Background(), cache, profile)
		if err != nil {
			t.Fatalf("an unresolvable ExcludeIPs host must not stop the VPN: %v", err)
		}
		old := &M.Metadata{Network: M.TCP, DstIP: netip.MustParseAddr("192.0.2.20"), DstPort: 445}
		if policy.IsBypass(old) {
			t.Fatal("the ExcludeIPs host was routed outside the VPN to its remembered address")
		}
	})
}
