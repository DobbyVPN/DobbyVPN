//go:build windows

package routing

import (
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"
)

func TestWindowsRouteLeaseUsesExactAddAndDeleteArguments(t *testing.T) {
	originalExists := windowsRouteExists
	originalCommand := windowsNetshCommand
	t.Cleanup(func() {
		windowsRouteExists = originalExists
		windowsNetshCommand = originalCommand
	})
	windowsRouteExists = func(windowsRoute) (bool, error) { return false, nil }
	var commands [][]string
	windowsNetshCommand = func(args ...string) (string, error) {
		commands = append(commands, append([]string(nil), args...))
		return "", nil
	}

	plan := NewPlan("lease-test")
	route := windowsRoute{prefix: "198.51.100.7/32", nextHop: "192.0.2.1", interfaceName: "Ethernet 2"}
	changed, err := acquireWindowsRoute(plan, "proxy route", route)
	if err != nil || !changed {
		t.Fatalf("acquire route changed=%v err=%v", changed, err)
	}
	if err := plan.Close(); err != nil {
		t.Fatalf("close plan: %v", err)
	}

	want := [][]string{
		windowsRouteArgs("add", route),
		windowsRouteArgs("delete", route),
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("netsh argv mismatch:\n got: %#v\nwant: %#v", commands, want)
	}
	for _, command := range commands {
		if strings.Contains(strings.Join(command, " "), " set ") || strings.HasPrefix(strings.Join(command, " "), "route delete") {
			t.Fatalf("unexpected route mutation command: %#v", command)
		}
	}
	if !strings.Contains(strings.Join(commands[0], " "), "metric=0") {
		t.Fatal("route acquisition must request the deterministic metric")
	}
	if strings.Contains(strings.Join(commands[1], " "), "metric=") {
		t.Fatal("route deletion must match by prefix, next hop, and interface after Windows normalizes metrics")
	}
}

func TestWindowsRouteLeasePreservesPreExistingExactRoute(t *testing.T) {
	originalExists := windowsRouteExists
	originalCommand := windowsNetshCommand
	t.Cleanup(func() {
		windowsRouteExists = originalExists
		windowsNetshCommand = originalCommand
	})
	windowsRouteExists = func(windowsRoute) (bool, error) { return true, nil }
	called := false
	windowsNetshCommand = func(args ...string) (string, error) {
		called = true
		return "", nil
	}

	plan := NewPlan("existing-route")
	changed, err := AcquireProxyRoute(plan, "198.51.100.7", "192.0.2.1", "Ethernet")
	if err != nil || changed {
		t.Fatalf("pre-existing route changed=%v err=%v", changed, err)
	}
	if err := plan.Close(); err != nil {
		t.Fatalf("close plan: %v", err)
	}
	if called {
		t.Fatal("pre-existing exact route must not be modified or deleted")
	}
}

func TestConfigureWindowsRoutingRollsBackLeasesLIFO(t *testing.T) {
	originalExists := windowsRouteExists
	originalCommand := windowsNetshCommand
	t.Cleanup(func() {
		windowsRouteExists = originalExists
		windowsNetshCommand = originalCommand
	})
	windowsRouteExists = func(windowsRoute) (bool, error) { return false, nil }
	var commands [][]string
	windowsNetshCommand = func(args ...string) (string, error) {
		commands = append(commands, append([]string(nil), args...))
		if args[2] == "add" && args[4] == "10.0.0.0/8" {
			return "", errors.New("injected add failure")
		}
		return "", nil
	}

	err := ConfigureWindowsRouting(NewPlan("rollback-test"), "198.51.100.7", "192.0.2.1", "dobbyvpn-wintun", "Ethernet")
	if err == nil {
		t.Fatal("expected configured add failure")
	}

	want := [][]string{
		windowsRouteArgs("add", windowsRoute{prefix: "198.51.100.7/32", nextHop: "192.0.2.1", interfaceName: "Ethernet"}),
		windowsRouteArgs("add", windowsRoute{prefix: "0.0.0.0/8", nextHop: "192.0.2.1", interfaceName: "Ethernet"}),
		windowsRouteArgs("add", windowsRoute{prefix: "10.0.0.0/8", nextHop: "192.0.2.1", interfaceName: "Ethernet"}),
		windowsRouteArgs("delete", windowsRoute{prefix: "0.0.0.0/8", nextHop: "192.0.2.1", interfaceName: "Ethernet"}),
		windowsRouteArgs("delete", windowsRoute{prefix: "198.51.100.7/32", nextHop: "192.0.2.1", interfaceName: "Ethernet"}),
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("rollback argv mismatch:\n got: %#v\nwant: %#v", commands, want)
	}
}

func TestConfigureWindowsRoutingSendsSplitDefaultRoutesOnLink(t *testing.T) {
	originalExists := windowsRouteExists
	originalCommand := windowsNetshCommand
	t.Cleanup(func() {
		windowsRouteExists = originalExists
		windowsNetshCommand = originalCommand
	})
	windowsRouteExists = func(windowsRoute) (bool, error) { return false, nil }
	var commands [][]string
	windowsNetshCommand = func(args ...string) (string, error) {
		commands = append(commands, append([]string(nil), args...))
		return "", nil
	}

	plan := NewPlan("tun-on-link-test")
	if err := ConfigureWindowsRouting(plan, "198.51.100.7", "192.0.2.1", "dobbyvpn-wintun", "Ethernet"); err != nil {
		t.Fatalf("configure routing: %v", err)
	}
	if err := plan.Close(); err != nil {
		t.Fatalf("close plan: %v", err)
	}

	for _, prefix := range ipv4Subnets {
		want := windowsRouteArgs("add", windowsRoute{prefix: prefix, nextHop: windowsOnLinkNextHop, interfaceName: "dobbyvpn-wintun"})
		found := false
		for _, command := range commands {
			if reflect.DeepEqual(command, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing on-link split default route for %s; commands=%#v", prefix, commands)
		}
	}
}

func TestSelectExactInterfaceNeverUsesSubstringMatch(t *testing.T) {
	interfaces := []net.Interface{{Name: "other-wintun"}, {Name: "dobbyvpn-wintun"}}
	iface, err := selectExactInterface("dobbyvpn-wintun", interfaces)
	if err != nil || iface.Name != "dobbyvpn-wintun" {
		t.Fatalf("exact selection iface=%v err=%v", iface, err)
	}
	if _, err := selectExactInterface("missing-wintun", interfaces); err == nil {
		t.Fatal("substring-compatible but non-exact adapter must not be selected")
	}
}
