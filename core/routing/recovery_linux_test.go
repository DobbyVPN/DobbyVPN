//go:build linux && !(android || ios)

package routing

import (
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func TestRecoverLinuxOwnedRoutesDoesNothingWithoutTaggedResources(t *testing.T) {
	fake := installLinuxNetlinkFake(t)
	if err := RecoverLinuxOwnedRoutes(233, 23333, "dobby233"); err != nil {
		t.Fatal(err)
	}
	if len(fake.routeDeletes) != 0 || len(fake.ruleDeletes) != 0 || len(fake.routeReplaces) != 0 {
		t.Fatalf("unexpected cleanup: route deletes=%#v rule deletes=%#v replacements=%#v", fake.routeDeletes, fake.ruleDeletes, fake.routeReplaces)
	}
	if len(fake.listedFamilies) != 2 || fake.listedFamilies[0] != netlink.FAMILY_V4 || fake.listedFamilies[1] != netlink.FAMILY_V6 {
		t.Fatalf("route-list families = %#v", fake.listedFamilies)
	}
}

func TestRecoverLinuxOwnedTerminalRouteAfterUplinkLoss(t *testing.T) {
	fake := installLinuxNetlinkFake(t)
	fake.routes = []netlink.Route{{
		Dst:      linuxDefaultIPNet(netlink.FAMILY_V4),
		Protocol: linuxOwnedRouteProtocol,
		Priority: 1,
		Family:   netlink.FAMILY_V4,
		Table:    233,
		Type:     unix.RTN_UNREACHABLE,
	}}
	fake.rules = append(fake.rules, *linuxSessionRule(233, 23333))

	if err := RecoverLinuxOwnedRoutes(233, 23333, "dobby233"); err != nil {
		t.Fatal(err)
	}
	if len(fake.ruleDeletes) != 1 || len(fake.routeDeletes) != 1 || fake.routeDeletes[0].Type != unix.RTN_UNREACHABLE {
		t.Fatalf("rule deletes=%#v route deletes=%#v", fake.ruleDeletes, fake.routeDeletes)
	}
}

func TestRecoverLinuxOwnedRoutesDeletesOnlyTypedTaggedResources(t *testing.T) {
	fake := installLinuxNetlinkFake(t)
	fake.links["eth0"] = linuxTestLink("eth0", 2)
	fake.links["dobby233"] = linuxTestLink("dobby233", 7)
	fake.routes = []netlink.Route{
		{
			Dst: linuxTestNetwork("198.51.100.8/32"), Gw: net.ParseIP("192.0.2.1"), LinkIndex: 2,
			Protocol: linuxOwnedRouteProtocol, Priority: linuxOwnedProxyMetric, Family: netlink.FAMILY_V4,
			Table: unix.RT_TABLE_MAIN, Type: unix.RTN_UNICAST,
		},
		{
			Dst: linuxDefaultIPNet(netlink.FAMILY_V4), LinkIndex: 7, Protocol: linuxOwnedRouteProtocol,
			Family: netlink.FAMILY_V4, Table: unix.RT_TABLE_MAIN, Type: unix.RTN_UNICAST,
		},
		{
			Dst: linuxTestNetwork("203.0.113.0/24"), Gw: net.ParseIP("192.0.2.1"), LinkIndex: 2,
			Protocol: linuxOwnedRouteProtocol, Priority: linuxOwnedProxyMetric, Family: netlink.FAMILY_V4,
			Table: unix.RT_TABLE_MAIN, Type: unix.RTN_UNICAST,
		},
		{
			Dst: linuxDefaultIPNet(netlink.FAMILY_V4), Gw: net.ParseIP("192.0.2.1"), LinkIndex: 2,
			Protocol: linuxOwnedRouteProtocol, Family: netlink.FAMILY_V4, Table: 233, Type: unix.RTN_UNICAST,
		},
		{
			Dst: linuxTestNetwork("::/1"), Protocol: linuxOwnedRouteProtocol, Priority: 1,
			Family: netlink.FAMILY_V6, Table: unix.RT_TABLE_MAIN, Type: unix.RTN_BLACKHOLE,
		},
		{
			Dst: linuxTestNetwork("8000::/1"), Protocol: linuxOwnedRouteProtocol, Priority: 1,
			Family: netlink.FAMILY_V6, Table: unix.RT_TABLE_MAIN, Type: unix.RTN_BLACKHOLE,
		},
		{
			Dst: linuxTestNetwork("2001:db8::/32"), Protocol: linuxOwnedRouteProtocol, Priority: 1,
			Family: netlink.FAMILY_V6, Table: unix.RT_TABLE_MAIN, Type: unix.RTN_BLACKHOLE,
		},
	}
	fake.rules = append(fake.rules, *linuxSessionRule(233, 23333))
	unrelatedRule := linuxSessionRule(233, 23334)
	fake.rules = append(fake.rules, *unrelatedRule)

	if err := RecoverLinuxOwnedRoutes(233, 23333, "dobby233"); err != nil {
		t.Fatal(err)
	}
	if len(fake.routeDeletes) != 5 {
		t.Fatalf("deleted routes = %#v, want proxy, TUN, marked, and two owned IPv6 routes", fake.routeDeletes)
	}
	if len(fake.ruleDeletes) != 1 || fake.ruleDeletes[0].Priority != 23333 {
		t.Fatalf("deleted rules = %#v", fake.ruleDeletes)
	}
	if len(fake.routeReplaces) != 1 || fake.routeReplaces[0].Gw.String() != "192.0.2.1" ||
		fake.routeReplaces[0].LinkIndex != 2 || fake.routeReplaces[0].Table != unix.RT_TABLE_MAIN {
		t.Fatalf("restored default route = %#v", fake.routeReplaces)
	}
}

func TestRecoverLinuxOwnedRoutesRestoresDefaultAfterTunDisappears(t *testing.T) {
	fake := installLinuxNetlinkFake(t)
	fake.routes = []netlink.Route{
		{
			Dst: linuxDefaultIPNet(netlink.FAMILY_V4), Gw: net.ParseIP("192.0.2.1"), LinkIndex: 2,
			Protocol: linuxOwnedRouteProtocol, Family: netlink.FAMILY_V4, Table: 233, Type: unix.RTN_UNICAST,
		},
	}

	if err := RecoverLinuxOwnedRoutes(233, 23333, "dobby233"); err != nil {
		t.Fatal(err)
	}
	if len(fake.routeReplaces) != 1 || fake.routeReplaces[0].Gw.String() != "192.0.2.1" {
		t.Fatalf("restored route = %#v", fake.routeReplaces)
	}
}

func TestRecoverLinuxOwnedRoutesPreservesUntaggedAndUnrecognizedRoutes(t *testing.T) {
	fake := installLinuxNetlinkFake(t)
	fake.routes = []netlink.Route{
		{
			Dst: linuxTestNetwork("198.51.100.8/32"), Gw: net.ParseIP("192.0.2.1"), LinkIndex: 2,
			Protocol: unix.RTPROT_STATIC, Priority: linuxOwnedProxyMetric, Family: netlink.FAMILY_V4,
			Table: unix.RT_TABLE_MAIN, Type: unix.RTN_UNICAST,
		},
		{
			Dst: linuxTestNetwork("203.0.113.0/24"), Gw: net.ParseIP("192.0.2.1"), LinkIndex: 2,
			Protocol: linuxOwnedRouteProtocol, Priority: linuxOwnedProxyMetric, Family: netlink.FAMILY_V4,
			Table: unix.RT_TABLE_MAIN, Type: unix.RTN_UNICAST,
		},
		{
			Dst: linuxDefaultIPNet(netlink.FAMILY_V4), Gw: net.ParseIP("192.0.2.254"), LinkIndex: 9,
			Protocol: unix.RTPROT_DHCP, Family: netlink.FAMILY_V4, Table: unix.RT_TABLE_MAIN, Type: unix.RTN_UNICAST,
		},
		{
			Dst: linuxDefaultIPNet(netlink.FAMILY_V4), Gw: net.ParseIP("192.0.2.1"), LinkIndex: 2,
			Protocol: unix.RTPROT_STATIC, Family: netlink.FAMILY_V4, Table: 233, Type: unix.RTN_UNICAST,
		},
		{
			Dst: linuxTestNetwork("2001:db8::/32"), Protocol: linuxOwnedRouteProtocol, Priority: 1,
			Family: netlink.FAMILY_V6, Table: unix.RT_TABLE_MAIN, Type: unix.RTN_BLACKHOLE,
		},
	}

	if err := RecoverLinuxOwnedRoutes(233, 23333, "dobby233"); err != nil {
		t.Fatal(err)
	}
	if len(fake.routeDeletes) != 0 || len(fake.ruleDeletes) != 0 || len(fake.routeReplaces) != 0 {
		t.Fatalf("unexpected cleanup: routes=%#v rules=%#v replacements=%#v", fake.routeDeletes, fake.ruleDeletes, fake.routeReplaces)
	}
}

func TestRecoverLinuxOwnedRoutesReportsRuleFailureAfterRouteCleanup(t *testing.T) {
	fake := installLinuxNetlinkFake(t)
	fake.routes = []netlink.Route{{
		Dst: linuxTestNetwork("198.51.100.8/32"), Gw: net.ParseIP("192.0.2.1"), LinkIndex: 2,
		Protocol: linuxOwnedRouteProtocol, Priority: linuxOwnedProxyMetric, Family: netlink.FAMILY_V4,
		Table: unix.RT_TABLE_MAIN, Type: unix.RTN_UNICAST,
	}}
	fake.rules = append(fake.rules, *linuxSessionRule(233, 23333))
	fake.ruleDeleteError = func(netlink.Rule) error { return errors.New("permission denied") }

	err := RecoverLinuxOwnedRoutes(233, 23333, "dobby233")
	if err == nil || !strings.Contains(err.Error(), "remove owned fwmark rule") {
		t.Fatalf("error = %v", err)
	}
	if len(fake.ruleDeletes) != 1 || len(fake.routeDeletes) != 1 {
		t.Fatalf("cleanup did not attempt both resources: rule deletes=%#v route deletes=%#v", fake.ruleDeletes, fake.routeDeletes)
	}
}

func TestRecoverLinuxOwnedRoutesReportsRuleListFailureAndStillDeletesRoutes(t *testing.T) {
	fake := installLinuxNetlinkFake(t)
	fake.routes = []netlink.Route{{
		Dst: linuxTestNetwork("198.51.100.8/32"), Gw: net.ParseIP("192.0.2.1"), LinkIndex: 2,
		Protocol: linuxOwnedRouteProtocol, Priority: linuxOwnedProxyMetric, Family: netlink.FAMILY_V4,
		Table: unix.RT_TABLE_MAIN, Type: unix.RTN_UNICAST,
	}}
	fake.ruleListError = errors.New("rule dump failed")

	err := RecoverLinuxOwnedRoutes(233, 23333, "dobby233")
	if err == nil || !strings.Contains(err.Error(), "inspect owned fwmark rule") || !strings.Contains(err.Error(), "rule dump failed") {
		t.Fatalf("error = %v", err)
	}
	if len(fake.routeDeletes) != 1 {
		t.Fatalf("route cleanup was skipped: %#v", fake.routeDeletes)
	}
}

func TestRecoverLinuxOwnedRoutesRemovesAnOrphanedExactRule(t *testing.T) {
	fake := installLinuxNetlinkFake(t)
	fake.rules = append(fake.rules, *linuxSessionRule(233, 23333))

	if err := RecoverLinuxOwnedRoutes(233, 23333, "dobby233"); err != nil {
		t.Fatal(err)
	}
	if len(fake.ruleDeletes) != 1 || len(fake.routeDeletes) != 0 {
		t.Fatalf("orphan rule cleanup: rules=%#v routes=%#v", fake.ruleDeletes, fake.routeDeletes)
	}
}
