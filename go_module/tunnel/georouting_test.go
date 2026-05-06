package tunnel

import (
	"net"
	"testing"
	"time"
)

func TestSetGeoRoutingConfReplacesRoutes(t *testing.T) {
	ClearGeoRoutingConf()
	t.Cleanup(ClearGeoRoutingConf)

	SetGeoRoutingConf("10.0.0.0/8")
	assertBypassRouteCount(t, 1)

	SetGeoRoutingConf("192.0.2.1/32")
	assertBypassRouteCount(t, 1)

	routesMu.RLock()
	defer routesMu.RUnlock()

	if !defaultBypassCIDRs[0].Contains(net.ParseIP("192.0.2.1")) {
		t.Fatalf("expected replacement route to contain 192.0.2.1, got %v", defaultBypassCIDRs)
	}
	if defaultBypassCIDRs[0].Contains(net.ParseIP("10.1.2.3")) {
		t.Fatalf("old route was retained after replacement: %v", defaultBypassCIDRs)
	}
}

func TestSetGeoRoutingConfResolvesHostWithoutDeadlock(t *testing.T) {
	ClearGeoRoutingConf()
	t.Cleanup(ClearGeoRoutingConf)

	done := make(chan struct{})
	go func() {
		SetGeoRoutingConf("localhost")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SetGeoRoutingConf deadlocked while resolving host entry")
	}

	routesMu.RLock()
	defer routesMu.RUnlock()

	if len(defaultBypassCIDRs) == 0 {
		t.Fatal("expected localhost to resolve to at least one bypass route")
	}
}

func assertBypassRouteCount(t *testing.T, expected int) {
	t.Helper()

	routesMu.RLock()
	defer routesMu.RUnlock()

	if len(defaultBypassCIDRs) != expected {
		t.Fatalf("expected %d bypass routes, got %d: %v", expected, len(defaultBypassCIDRs), defaultBypassCIDRs)
	}
}
