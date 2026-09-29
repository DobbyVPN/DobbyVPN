//go:build linux && !(android || ios)

package routing

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func TestLinuxProxyRouteLeasePreservesExistingRoute(t *testing.T) {
	fake := installLinuxNetlinkFake(t)
	fake.links["eth0"] = linuxTestLink("eth0", 2)
	fake.routeAddError = func(netlink.Route) error { return unix.EEXIST }

	plan := NewPlan("generation-19")
	lease, err := plan.AcquireLinuxProxyRoute("198.51.100.8", "192.0.2.1", "eth0")
	if err != nil {
		t.Fatal(err)
	}
	if lease == nil {
		t.Fatal("expected lease")
	}
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	if len(fake.routeAdds) != 1 || len(fake.routeDeletes) != 0 {
		t.Fatalf("route additions=%#v deletions=%#v; existing route must not be deleted", fake.routeAdds, fake.routeDeletes)
	}
	if fake.routeAdds[0].Dst.String() != "198.51.100.8/32" || fake.routeAdds[0].Priority != linuxOwnedProxyMetric {
		t.Fatalf("proxy route = %#v", fake.routeAdds[0])
	}
}

func TestLinuxProxyRouteCleanupToleratesLinkRemovedRoute(t *testing.T) {
	fake := installLinuxNetlinkFake(t)
	fake.links["eth0"] = linuxTestLink("eth0", 2)
	fake.routeDeleteError = func(netlink.Route) error { return unix.ESRCH }

	plan := NewPlan("generation-proxy-link-loss")
	if _, err := plan.AcquireLinuxProxyRoute("198.51.100.8", "192.0.2.1", "eth0"); err != nil {
		t.Fatal(err)
	}
	if err := plan.Close(); err != nil {
		t.Fatalf("cleanup should be idempotent after link removal: %v", err)
	}
}

func TestLinuxMarkedRoutingCleanupToleratesLinkRemovedRoutes(t *testing.T) {
	fake := installLinuxNetlinkFake(t)
	fake.links["eth0"] = linuxTestLink("eth0", 2)
	fake.routeDeleteError = func(netlink.Route) error { return unix.ESRCH }

	plan := NewPlan("generation-mark-link-loss")
	if err := plan.AcquireLinuxMarkedRouting(233, 23333, "eth0", "192.0.2.1"); err != nil {
		t.Fatal(err)
	}
	if err := plan.Close(); err != nil {
		t.Fatalf("cleanup should be idempotent after link removal: %v", err)
	}
}

func TestLinuxMarkedRoutingDoesNotClaimAnExistingFwmarkRule(t *testing.T) {
	fake := installLinuxNetlinkFake(t)
	fake.links["eth0"] = linuxTestLink("eth0", 2)
	fake.rules = append(fake.rules, *linuxSessionRule(233, 23333))

	plan := NewPlan("generation-mark-existing-rule")
	if err := plan.AcquireLinuxMarkedRouting(233, 23333, "eth0", "192.0.2.1"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("AcquireLinuxMarkedRouting error = %v, want existing-rule failure", err)
	}
	if len(fake.routeAdds) != 0 || len(fake.ruleAdds) != 0 || len(fake.routeDeletes) != 0 {
		t.Fatalf("existing rule was claimed or routing changed: routes=%#v rules=%#v deletes=%#v", fake.routeAdds, fake.ruleAdds, fake.routeDeletes)
	}
}

func TestLinuxTunnelDefaultRestoresCapturedBaseline(t *testing.T) {
	fake := installLinuxNetlinkFake(t)
	fake.links["eth0"] = linuxTestLink("eth0", 2)
	fake.links["dobby0"] = linuxTestLink("dobby0", 7)
	baseline := netlink.Route{
		Dst:       linuxTestNetwork("0.0.0.0/0"),
		Gw:        []byte{192, 0, 2, 1},
		LinkIndex: 2,
		Protocol:  16,
		Priority:  100,
		Family:    netlink.FAMILY_V4,
		Table:     unix.RT_TABLE_MAIN,
		Type:      unix.RTN_UNICAST,
		Scope:     netlink.SCOPE_UNIVERSE,
	}
	fake.routes = []netlink.Route{baseline}

	plan := NewPlan("generation-20")
	if _, err := plan.AcquireLinuxTunnelDefault("dobby0"); err != nil {
		t.Fatal(err)
	}
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	if len(fake.routeReplaces) != 2 || len(fake.routeDeletes) != 1 {
		t.Fatalf("route replacements=%#v deletions=%#v", fake.routeReplaces, fake.routeDeletes)
	}
	tunnel := fake.routeReplaces[0]
	if tunnel.LinkIndex != 7 || tunnel.Protocol != linuxOwnedRouteProtocol || !linuxRouteIsDefault(tunnel, netlink.FAMILY_V4) {
		t.Fatalf("installed TUN default = %#v", tunnel)
	}
	if !reflect.DeepEqual(fake.routeReplaces[1], baseline) {
		t.Fatalf("restored default = %#v, want captured route %#v", fake.routeReplaces[1], baseline)
	}
}

func TestLinuxResolvedDNSLeaseConfiguresAndRevertsOnlyTunnelLink(t *testing.T) {
	original := linuxRunResolvedCommand
	t.Cleanup(func() { linuxRunResolvedCommand = original })
	var commands [][]string
	linuxRunResolvedCommand = func(args ...string) error {
		commands = append(commands, append([]string(nil), args...))
		return nil
	}

	plan := NewPlan("generation-dns")
	if _, err := plan.AcquireLinuxResolvedDNS("dobby0", "9.9.9.9"); err != nil {
		t.Fatal(err)
	}
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"dns", "dobby0", "9.9.9.9"},
		{"domain", "dobby0", "~."},
		{"default-route", "dobby0", "yes"},
		{"revert", "dobby0"},
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %v, want %v", commands, want)
	}
}

func TestLinuxResolvedDNSFailureRevertsPartialConfiguration(t *testing.T) {
	original := linuxRunResolvedCommand
	t.Cleanup(func() { linuxRunResolvedCommand = original })
	var commands [][]string
	linuxRunResolvedCommand = func(args ...string) error {
		commands = append(commands, append([]string(nil), args...))
		if args[0] == "domain" {
			return errors.New("permission denied")
		}
		return nil
	}

	plan := NewPlan("generation-dns-failure")
	if _, err := plan.AcquireLinuxResolvedDNS("dobby0", "9.9.9.9"); err == nil {
		t.Fatal("AcquireLinuxResolvedDNS succeeded")
	}
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"dns", "dobby0", "9.9.9.9"},
		{"domain", "dobby0", "~."},
		{"revert", "dobby0"},
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %v, want %v", commands, want)
	}
}

func TestLinuxMarkedRoutingFailureRollsBackOnlyRoutesCreatedByPlan(t *testing.T) {
	fake := installLinuxNetlinkFake(t)
	fake.links["eth0"] = linuxTestLink("eth0", 2)
	fake.ruleAddError = func(netlink.Rule) error { return errors.New("permission denied") }

	plan := NewPlan("generation-23")
	if err := plan.AcquireLinuxMarkedRouting(233, 23333, "eth0", "192.0.2.1"); err == nil {
		t.Fatal("AcquireLinuxMarkedRouting succeeded")
	}
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	if len(fake.routeAdds) != 2 || len(fake.ruleAdds) != 1 || len(fake.routeDeletes) != 2 {
		t.Fatalf("route adds=%#v rule adds=%#v route deletes=%#v", fake.routeAdds, fake.ruleAdds, fake.routeDeletes)
	}
	if fake.routeDeletes[0].Type != unix.RTN_UNREACHABLE || fake.routeDeletes[1].Table != 233 ||
		fake.routeDeletes[1].Gw.String() != "192.0.2.1" {
		t.Fatalf("rollback order/routes = %#v", fake.routeDeletes)
	}
}

func TestLinuxTunnelDefaultDoesNotRestoreBaselineWhenOwnedRouteIsGone(t *testing.T) {
	fake := installLinuxNetlinkFake(t)
	fake.links["eth0"] = linuxTestLink("eth0", 2)
	fake.links["dobby0"] = linuxTestLink("dobby0", 7)
	fake.routes = []netlink.Route{{
		Dst:       linuxTestNetwork("0.0.0.0/0"),
		Gw:        []byte{192, 0, 2, 1},
		LinkIndex: 2,
		Priority:  100,
		Family:    netlink.FAMILY_V4,
		Table:     unix.RT_TABLE_MAIN,
		Type:      unix.RTN_UNICAST,
	}}
	fake.routeDeleteError = func(route netlink.Route) error {
		if route.LinkIndex == 7 {
			return unix.ESRCH
		}
		return nil
	}

	plan := NewPlan("generation-24")
	if _, err := plan.AcquireLinuxTunnelDefault("dobby0"); err != nil {
		t.Fatal(err)
	}
	if err := plan.Close(); err == nil {
		t.Fatal("Close succeeded after session-owned route was already gone")
	}
	if len(fake.routeReplaces) != 1 {
		t.Fatalf("restored baseline after failing to remove owned route: %#v", fake.routeReplaces)
	}
}

func TestLinuxIPv6BlockPreservesExistingRoutes(t *testing.T) {
	fake := installLinuxNetlinkFake(t)
	fake.routeAddError = func(netlink.Route) error { return unix.EEXIST }

	plan := NewPlan("generation-25")
	if err := plan.AcquireLinuxIPv6Block(); err != nil {
		t.Fatal(err)
	}
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	if len(fake.routeAdds) != 2 || len(fake.routeDeletes) != 0 {
		t.Fatalf("IPv6 route adds=%#v deletes=%#v", fake.routeAdds, fake.routeDeletes)
	}
	if fake.routeAdds[0].Type != unix.RTN_BLACKHOLE || fake.routeAdds[0].Protocol != linuxOwnedRouteProtocol ||
		!strings.HasSuffix(fake.routeAdds[0].Dst.String(), "/1") {
		t.Fatalf("IPv6 block route = %#v", fake.routeAdds[0])
	}
}
