package runtime

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"core/dnscache"
	"core/sessionapi"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"
)

// Start holds the runtime lock while it resolves bypass hosts. A reported
// network change must reach the cache without waiting for that lock.
func TestNetworkChangedDoesNotWaitForRuntimeLock(t *testing.T) {
	r := &runtime{}
	cache := dnscache.New()
	t.Cleanup(cache.Close)
	r.cache.Store(cache)
	r.mu.Lock()
	defer r.mu.Unlock()
	done := make(chan struct{})
	go func() {
		r.NetworkChanged()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("NetworkChanged waited for the runtime lock")
	}
}

// An ExcludeIPs host name whose answer is remembered from an earlier lookup
// is not routed outside the VPN when the router's DNS stops answering (#507):
// the connection resolves ExcludeIPs without remembered answers.
func TestExcludedHostIsNotBypassedWithRememberedAnswerWhenDNSStops(t *testing.T) {
	cache := dnscache.New()
	t.Cleanup(cache.Close)
	answered := false
	cache.SetResolver("home-wifi", func(context.Context, string) ([]net.IPAddr, error) {
		if answered {
			return nil, context.DeadlineExceeded
		}
		answered = true
		return []net.IPAddr{{IP: net.ParseIP("192.0.2.20")}}, nil
	})
	if _, err := cache.ResolveAddresses(context.Background(), "nas.example"); err != nil {
		t.Fatal(err)
	}
	policy, err := resolveBypassPolicy(context.Background(), cache, sessionapi.RuntimeProfile{ExcludeHosts: []string{"nas.example"}})
	if err != nil {
		t.Fatal(err)
	}
	if policy.IsBypass(&M.Metadata{Network: M.TCP, DstIP: netip.MustParseAddr("192.0.2.20"), DstPort: 445}) {
		t.Fatal("a remembered ExcludeIPs address must not bypass the VPN")
	}
}
