package tunnel

import (
	"reflect"
	"testing"
)

func TestGeoRoutingLeaseRestoresExactBaseline(t *testing.T) {
	setTestGeoRoutingConf(t, "192.0.2.0/24", "2001:db8::/32")

	lease, err := AcquireGeoRoutingConf([]string{"198.51.100.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	if got := currentGeoRoutingCIDRs(); !reflect.DeepEqual(got, []string{"198.51.100.0/24"}) {
		t.Fatalf("session policy = %#v", got)
	}

	lease.Release()
	lease.Release()
	if got := currentGeoRoutingCIDRs(); !reflect.DeepEqual(got, []string{"192.0.2.0/24", "2001:db8::/32"}) {
		t.Fatalf("restored policy = %#v", got)
	}
}

func TestGeoRoutingLeaseCopiesCallerInput(t *testing.T) {
	setTestGeoRoutingConf(t)
	entries := []string{"198.51.100.0/24"}
	lease, err := AcquireGeoRoutingConf(entries)
	if err != nil {
		t.Fatal(err)
	}
	entries[0] = "203.0.113.0/24"
	if got := currentGeoRoutingCIDRs(); !reflect.DeepEqual(got, []string{"198.51.100.0/24"}) {
		t.Fatalf("policy changed with caller slice = %#v", got)
	}
	lease.Release()
}

func setTestGeoRoutingConf(t *testing.T, entries ...string) {
	t.Helper()
	routes, err := resolveRoutingEntries(entries)
	if err != nil {
		t.Fatal(err)
	}
	routesMu.Lock()
	defaultBypassCIDRs = cloneIPNets(routes)
	routesMu.Unlock()
	t.Cleanup(func() {
		routesMu.Lock()
		defaultBypassCIDRs = nil
		routesMu.Unlock()
	})
}

func currentGeoRoutingCIDRs() []string {
	routesMu.RLock()
	defer routesMu.RUnlock()
	result := make([]string, 0, len(defaultBypassCIDRs))
	for _, network := range defaultBypassCIDRs {
		result = append(result, network.String())
	}
	return result
}
