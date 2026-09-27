package platform_engine

import (
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"
)

func TestRecoverStaleWindowsResourcesUsesExactNamesAndCleansRoutesBeforeAdapters(t *testing.T) {
	second := "DobbyVPN-22222222222222222222222222222222"
	first := "DobbyVPN-11111111111111111111111111111111"
	var events []string
	err := recoverStaleWindowsResources(
		[]net.Interface{
			{Name: second},
			{Name: "Ethernet"},
			{Name: "DobbyVPN-short"},
			{Name: first},
			{Name: first},
		},
		func(name string) error {
			events = append(events, "routes:"+name)
			return nil
		},
		func() error {
			events = append(events, "firewall")
			return nil
		},
		func(name string) error {
			events = append(events, "adapter:"+name)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"routes:" + first,
		"routes:" + second,
		"firewall",
		"adapter:" + first,
		"adapter:" + second,
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("recovery operations=%v, want=%v", events, want)
	}
}

func TestRecoverStaleWindowsResourcesFailsClosedBeforeAdapterRemoval(t *testing.T) {
	name := "DobbyVPN-11111111111111111111111111111111"
	routeErr := errors.New("route remains")
	var removed bool
	err := recoverStaleWindowsResources(
		[]net.Interface{{Name: name}},
		func(string) error { return routeErr },
		func() error { return nil },
		func(string) error {
			removed = true
			return nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "clean stale routes") || !errors.Is(err, routeErr) {
		t.Fatalf("recovery error=%v, want wrapped route cleanup error", err)
	}
	if removed {
		t.Fatal("adapter was removed after its exact routes could not be verified absent")
	}
}

func TestRecoverStaleWindowsResourcesFirewallFailureBlocksAdapterRemoval(t *testing.T) {
	name := "DobbyVPN-11111111111111111111111111111111"
	firewallErr := errors.New("firewall cleanup denied")
	var removed bool
	err := recoverStaleWindowsResources(
		[]net.Interface{{Name: name}},
		func(string) error { return nil },
		func() error { return firewallErr },
		func(string) error {
			removed = true
			return nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "IPv6 firewall") || !errors.Is(err, firewallErr) {
		t.Fatalf("recovery error=%v, want wrapped firewall cleanup error", err)
	}
	if removed {
		t.Fatal("adapter was removed before all stale routing resources were cleaned")
	}
}

func TestRecoverStaleWindowsResourcesCleansRulesWithoutOwnedAdapters(t *testing.T) {
	called := false
	err := recoverStaleWindowsResources(
		[]net.Interface{{Name: "Ethernet"}},
		func(string) error { t.Fatal("unexpected route cleanup"); return nil },
		func() error { called = true; return nil },
		func(string) error { t.Fatal("unexpected adapter removal"); return nil },
	)
	if err != nil || !called {
		t.Fatalf("firewall cleanup called=%t err=%v", called, err)
	}
}
