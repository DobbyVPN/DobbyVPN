//go:build windows

package routing

import (
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
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

func TestWindowsRouteLeaseFailsWhenDeletedRouteRemains(t *testing.T) {
	originalExists := windowsRouteExists
	originalCommand := windowsNetshCommand
	t.Cleanup(func() {
		windowsRouteExists = originalExists
		windowsNetshCommand = originalCommand
	})
	windowsNetshCommand = func(...string) (string, error) { return "", nil }
	windowsRouteExists = func(windowsRoute) (bool, error) { return true, nil }

	err := releaseWindowsRoute(
		windowsRoute{prefix: "198.51.100.7/32", nextHop: "192.0.2.1", interfaceName: "Ethernet"},
		5*time.Millisecond,
	)
	if err == nil || !strings.Contains(err.Error(), "remained after deletion") {
		t.Fatalf("release error=%v", err)
	}
}

func TestWindowsRouteLeaseAcceptsVerifiedDeletion(t *testing.T) {
	originalExists := windowsRouteExists
	originalCommand := windowsNetshCommand
	t.Cleanup(func() {
		windowsRouteExists = originalExists
		windowsNetshCommand = originalCommand
	})
	windowsNetshCommand = func(...string) (string, error) { return "", nil }
	windowsRouteExists = func(windowsRoute) (bool, error) { return false, nil }

	if err := releaseWindowsRoute(
		windowsRoute{prefix: "198.51.100.7/32", nextHop: "192.0.2.1", interfaceName: "Ethernet"},
		5*time.Millisecond,
	); err != nil {
		t.Fatalf("release route: %v", err)
	}
}

func TestWindowsRouteLeaseAcceptsAlreadyAbsentRouteAfterDeleteError(t *testing.T) {
	originalExists := windowsRouteExists
	originalCommand := windowsNetshCommand
	t.Cleanup(func() {
		windowsRouteExists = originalExists
		windowsNetshCommand = originalCommand
	})
	windowsNetshCommand = func(...string) (string, error) {
		return "Element not found.", errors.New("exit status 1")
	}
	windowsRouteExists = func(windowsRoute) (bool, error) { return false, nil }

	if err := releaseWindowsRoute(
		windowsRoute{prefix: "198.51.100.7/32", nextHop: "192.0.2.1", interfaceName: "Ethernet"},
		5*time.Millisecond,
	); err != nil {
		t.Fatalf("release route already absent after adapter reset: %v", err)
	}
}

func TestWindowsRouteLeaseRejectsDeleteErrorWhenRouteRemains(t *testing.T) {
	originalExists := windowsRouteExists
	originalCommand := windowsNetshCommand
	t.Cleanup(func() {
		windowsRouteExists = originalExists
		windowsNetshCommand = originalCommand
	})
	windowsNetshCommand = func(...string) (string, error) {
		return "Access denied.", errors.New("exit status 1")
	}
	windowsRouteExists = func(windowsRoute) (bool, error) { return true, nil }

	err := releaseWindowsRoute(
		windowsRoute{prefix: "198.51.100.7/32", nextHop: "192.0.2.1", interfaceName: "Ethernet"},
		5*time.Millisecond,
	)
	if err == nil || !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("release error=%v", err)
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

func TestCleanupStaleWindowsTunnelRoutesDeletesOnlyOwnedSplitDefaults(t *testing.T) {
	originalExists := windowsRouteExists
	originalCommand := windowsNetshCommand
	t.Cleanup(func() {
		windowsRouteExists = originalExists
		windowsNetshCommand = originalCommand
	})
	interfaceName := "DobbyVPN-0123456789abcdef0123456789abcdef"
	state := map[windowsRoute]bool{
		{prefix: "0.0.0.0/1", nextHop: "0.0.0.0", interfaceName: interfaceName}:   true,
		{prefix: "128.0.0.0/1", nextHop: "0.0.0.0", interfaceName: interfaceName}: true,
	}
	windowsRouteExists = func(route windowsRoute) (bool, error) { return state[route], nil }
	var commands [][]string
	windowsNetshCommand = func(args ...string) (string, error) {
		commands = append(commands, append([]string(nil), args...))
		if len(args) >= 7 && args[2] == "delete" {
			route := windowsRoute{
				prefix:        args[4],
				nextHop:       strings.TrimPrefix(args[5], "nexthop="),
				interfaceName: strings.TrimPrefix(args[6], "interface="),
			}
			delete(state, route)
		}
		return "", nil
	}

	if err := CleanupStaleWindowsTunnelRoutes(interfaceName); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		windowsRouteArgs("delete", windowsRoute{prefix: "0.0.0.0/1", nextHop: "0.0.0.0", interfaceName: interfaceName}),
		windowsRouteArgs("delete", windowsRoute{prefix: "128.0.0.0/1", nextHop: "0.0.0.0", interfaceName: interfaceName}),
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("stale route deletions=%#v, want=%#v", commands, want)
	}
}

func TestCleanupStaleWindowsTunnelRoutesRejectsUnownedInterface(t *testing.T) {
	originalCommand := windowsNetshCommand
	t.Cleanup(func() { windowsNetshCommand = originalCommand })
	called := false
	windowsNetshCommand = func(...string) (string, error) {
		called = true
		return "", nil
	}
	if err := CleanupStaleWindowsTunnelRoutes("Ethernet"); err == nil {
		t.Fatal("stale route cleanup accepted a physical interface")
	}
	if called {
		t.Fatal("stale route cleanup mutated a physical interface")
	}
}

func TestCleanupStaleWindowsIPv6FirewallRulesUsesExactPersistentRuleNames(t *testing.T) {
	original := windowsPowerShellCommand
	t.Cleanup(func() { windowsPowerShellCommand = original })
	var script string
	var timeout time.Duration
	windowsPowerShellCommand = func(gotScript string, gotTimeout time.Duration) (string, string, error) {
		script = gotScript
		timeout = gotTimeout
		return "DobbyVPN Block IPv6 windows-1790525851930782900-2\r\n", "", nil
	}
	if err := CleanupStaleWindowsIPv6FirewallRules(); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"-DisplayName $displayName -PolicyStore PersistentStore",
		"^DobbyVPN Block IPv6 windows-[0-9]+-[0-9]+$",
		"Remove-NetFirewallRule -InputObject $rule -ErrorAction Stop",
		"$remaining.Count -gt 0",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("cleanup script does not contain %q: %s", required, script)
		}
	}
	if timeout != 30*time.Second {
		t.Errorf("PowerShell timeout=%s, want 30s", timeout)
	}
}

func TestCleanupStaleWindowsIPv6FirewallRulesPreservesCommandOutputOnError(t *testing.T) {
	original := windowsPowerShellCommand
	t.Cleanup(func() { windowsPowerShellCommand = original })
	windowsPowerShellCommand = func(string, time.Duration) (string, string, error) {
		return "stdout diagnostic", "stderr diagnostic", errors.New("command failed")
	}
	err := CleanupStaleWindowsIPv6FirewallRules()
	if err == nil || !strings.Contains(err.Error(), "stdout: stdout diagnostic") || !strings.Contains(err.Error(), "stderr: stderr diagnostic") {
		t.Fatalf("cleanup error=%v, want complete stdout/stderr diagnostics", err)
	}
}
