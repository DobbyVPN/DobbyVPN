//go:build linux && !(android || ios)

package routing

import (
	"errors"
	"net"
	"reflect"
	"testing"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

type linuxNetlinkFake struct {
	routes []netlink.Route
	rules  []netlink.Rule
	links  map[string]netlink.Link

	listedFamilies []int
	routeAdds      []netlink.Route
	routeReplaces  []netlink.Route
	routeDeletes   []netlink.Route
	ruleAdds       []netlink.Rule
	ruleDeletes    []netlink.Rule

	routeListError     error
	ipv6RouteListError error
	ruleListError      error
	routeAddError      func(netlink.Route) error
	routeReplaceError  func(netlink.Route) error
	routeDeleteError   func(netlink.Route) error
	ruleAddError       func(netlink.Rule) error
	ruleDeleteError    func(netlink.Rule) error
}

func installLinuxNetlinkFake(t *testing.T) *linuxNetlinkFake {
	t.Helper()
	fake := &linuxNetlinkFake{links: map[string]netlink.Link{}}
	originalRouteList := linuxRouteList
	originalRouteAdd := linuxRouteAdd
	originalRouteReplace := linuxRouteReplace
	originalRouteDel := linuxRouteDel
	originalRuleList := linuxRuleList
	originalRuleAdd := linuxRuleAdd
	originalRuleDel := linuxRuleDel
	originalLinkByName := linuxLinkByName
	originalLinkByIndex := linuxLinkByIndex
	t.Cleanup(func() {
		linuxRouteList = originalRouteList
		linuxRouteAdd = originalRouteAdd
		linuxRouteReplace = originalRouteReplace
		linuxRouteDel = originalRouteDel
		linuxRuleList = originalRuleList
		linuxRuleAdd = originalRuleAdd
		linuxRuleDel = originalRuleDel
		linuxLinkByName = originalLinkByName
		linuxLinkByIndex = originalLinkByIndex
	})

	linuxRouteList = func(family int) ([]netlink.Route, error) {
		fake.listedFamilies = append(fake.listedFamilies, family)
		if family == netlink.FAMILY_V6 && fake.ipv6RouteListError != nil {
			return nil, fake.ipv6RouteListError
		}
		if fake.routeListError != nil {
			return nil, fake.routeListError
		}
		var routes []netlink.Route
		for _, route := range fake.routes {
			if route.Family == family {
				routes = append(routes, cloneLinuxTestRoute(route))
			}
		}
		return routes, nil
	}
	linuxRouteAdd = func(route *netlink.Route) error {
		copy := cloneLinuxTestRoute(*route)
		fake.routeAdds = append(fake.routeAdds, copy)
		if fake.routeAddError != nil {
			if err := fake.routeAddError(copy); err != nil {
				return err
			}
		}
		fake.routes = append(fake.routes, copy)
		return nil
	}
	linuxRouteReplace = func(route *netlink.Route) error {
		copy := cloneLinuxTestRoute(*route)
		fake.routeReplaces = append(fake.routeReplaces, copy)
		if fake.routeReplaceError != nil {
			if err := fake.routeReplaceError(copy); err != nil {
				return err
			}
		}
		fake.routes = append(fake.routes, copy)
		return nil
	}
	linuxRouteDel = func(route *netlink.Route) error {
		copy := cloneLinuxTestRoute(*route)
		fake.routeDeletes = append(fake.routeDeletes, copy)
		if fake.routeDeleteError != nil {
			if err := fake.routeDeleteError(copy); err != nil {
				return err
			}
		}
		for index, existing := range fake.routes {
			if reflect.DeepEqual(existing, copy) {
				fake.routes = append(fake.routes[:index], fake.routes[index+1:]...)
				break
			}
		}
		return nil
	}
	linuxRuleList = func(family int) ([]netlink.Rule, error) {
		if fake.ruleListError != nil {
			return nil, fake.ruleListError
		}
		var rules []netlink.Rule
		for _, rule := range fake.rules {
			if rule.Family == family {
				rules = append(rules, rule)
			}
		}
		return rules, nil
	}
	linuxRuleAdd = func(rule *netlink.Rule) error {
		copy := *rule
		fake.ruleAdds = append(fake.ruleAdds, copy)
		if fake.ruleAddError != nil {
			if err := fake.ruleAddError(copy); err != nil {
				return err
			}
		}
		fake.rules = append(fake.rules, copy)
		return nil
	}
	linuxRuleDel = func(rule *netlink.Rule) error {
		copy := *rule
		fake.ruleDeletes = append(fake.ruleDeletes, copy)
		if fake.ruleDeleteError != nil {
			if err := fake.ruleDeleteError(copy); err != nil {
				return err
			}
		}
		for index, existing := range fake.rules {
			if reflect.DeepEqual(existing, copy) {
				fake.rules = append(fake.rules[:index], fake.rules[index+1:]...)
				break
			}
		}
		return nil
	}
	linuxLinkByName = func(name string) (netlink.Link, error) {
		for _, link := range fake.links {
			if link.Attrs().Name == name {
				return link, nil
			}
		}
		return nil, errors.New("link not found")
	}
	linuxLinkByIndex = func(index int) (netlink.Link, error) {
		for _, link := range fake.links {
			if link.Attrs().Index == index {
				return link, nil
			}
		}
		return nil, errors.New("link not found")
	}
	return fake
}

func linuxTestLink(name string, index int) netlink.Link {
	return &netlink.Device{LinkAttrs: netlink.LinkAttrs{Name: name, Index: index}}
}

func cloneLinuxTestRoute(route netlink.Route) netlink.Route {
	if route.Dst != nil {
		route.Dst = &net.IPNet{IP: append(net.IP(nil), route.Dst.IP...), Mask: append(net.IPMask(nil), route.Dst.Mask...)}
	}
	if route.Gw != nil {
		route.Gw = append(net.IP(nil), route.Gw...)
	}
	if route.Src != nil {
		route.Src = append(net.IP(nil), route.Src...)
	}
	return route
}

func linuxTestNetwork(cidr string) *net.IPNet {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err)
	}
	return network
}

func TestDiscoverLinuxDefaultRouteUsesGatewayBackedMainTableRoute(t *testing.T) {
	fake := installLinuxNetlinkFake(t)
	fake.links["eth0"] = linuxTestLink("eth0", 2)
	fake.routes = []netlink.Route{
		{Dst: linuxDefaultIPNet(netlink.FAMILY_V4), LinkIndex: 7, Family: netlink.FAMILY_V4, Table: unix.RT_TABLE_MAIN, Type: unix.RTN_UNICAST},
		{Dst: linuxDefaultIPNet(netlink.FAMILY_V4), Gw: net.ParseIP("192.0.2.1"), LinkIndex: 2, Priority: 100, Family: netlink.FAMILY_V4, Table: unix.RT_TABLE_MAIN, Type: unix.RTN_UNICAST},
		{Dst: linuxDefaultIPNet(netlink.FAMILY_V4), Gw: net.ParseIP("192.0.2.9"), LinkIndex: 9, Family: 0, Table: unix.RT_TABLE_MAIN, Type: unix.RTN_UNICAST},
		{Dst: linuxDefaultIPNet(netlink.FAMILY_V4), Gw: net.ParseIP("192.0.2.8"), LinkIndex: 8, Family: netlink.FAMILY_V4, Table: 100, Type: unix.RTN_UNICAST},
	}

	gateway, iface, err := DiscoverLinuxDefaultRoute()
	if err != nil {
		t.Fatal(err)
	}
	if gateway != "192.0.2.1" || iface != "eth0" {
		t.Fatalf("default route = %s/%s, want 192.0.2.1/eth0", gateway, iface)
	}
}

func TestDiscoverLinuxDefaultRoutePreservesNetlinkDiagnostic(t *testing.T) {
	fake := installLinuxNetlinkFake(t)
	fake.routeListError = errors.New("route list unavailable")
	if _, _, err := DiscoverLinuxDefaultRoute(); err == nil || err.Error() != "discover main-table default route: route list unavailable" {
		t.Fatalf("error = %v, want wrapped netlink diagnostic", err)
	}
}

func TestReconcileLinuxSessionRoutesUsesTypedRoutesAndRule(t *testing.T) {
	fake := installLinuxNetlinkFake(t)
	fake.links["eth0"] = linuxTestLink("eth0", 2)
	if err := ReconcileLinuxSessionRoutesWithRule("198.51.100.9", "192.0.2.1", "eth0", 233, 23333); err != nil {
		t.Fatal(err)
	}
	if len(fake.routeReplaces) != 2 {
		t.Fatalf("route replacements = %#v, want proxy and marked default", fake.routeReplaces)
	}
	proxy := fake.routeReplaces[0]
	if proxy.Dst.String() != "198.51.100.9/32" || proxy.Gw.String() != "192.0.2.1" || proxy.LinkIndex != 2 ||
		proxy.Table != unix.RT_TABLE_MAIN || proxy.Protocol != linuxOwnedRouteProtocol || proxy.Priority != linuxOwnedProxyMetric {
		t.Fatalf("proxy route = %#v", proxy)
	}
	marked := fake.routeReplaces[1]
	if !linuxRouteIsDefault(marked, netlink.FAMILY_V4) || marked.Gw.String() != "192.0.2.1" ||
		marked.Table != 233 || marked.LinkIndex != 2 || marked.Protocol != linuxOwnedRouteProtocol {
		t.Fatalf("marked route = %#v", marked)
	}
	if len(fake.ruleAdds) != 1 || !linuxSessionRuleMatches(fake.ruleAdds[0], 233, 23333) {
		t.Fatalf("fwmark rules = %#v", fake.ruleAdds)
	}
}

func TestReconcileLinuxSessionRoutesAcceptsOnlyTheExistingExactRule(t *testing.T) {
	fake := installLinuxNetlinkFake(t)
	fake.links["eth0"] = linuxTestLink("eth0", 2)
	fake.ruleAddError = func(netlink.Rule) error { return unix.EEXIST }
	fake.rules = append(fake.rules, *linuxSessionRule(233, 23333))
	if err := ReconcileLinuxSessionRoutesWithRule("127.0.0.1", "192.0.2.1", "eth0", 233, 23333); err != nil {
		t.Fatalf("existing exact rule should be harmless: %v", err)
	}

	fake.ruleAddError = func(netlink.Rule) error { return unix.EEXIST }
	fake.rules = []netlink.Rule{*linuxSessionRule(233, 23334)}
	if err := ReconcileLinuxSessionRoutesWithRule("127.0.0.1", "192.0.2.1", "eth0", 233, 23333); err == nil {
		t.Fatal("different rule at the requested priority was accepted")
	}
}

func TestLinuxRouteHelpersRecognizeTypedDefaultsAndNetworks(t *testing.T) {
	route := netlink.Route{Dst: linuxTestNetwork("0.0.0.0/0"), Family: netlink.FAMILY_V4}
	if !linuxRouteIsDefault(route, netlink.FAMILY_V4) {
		t.Fatal("IPv4 default route was not recognized")
	}
	if linuxRouteIsDefault(netlink.Route{Dst: linuxTestNetwork("192.0.2.0/24"), Family: netlink.FAMILY_V4}, netlink.FAMILY_V4) {
		t.Fatal("non-default route was recognized as a default")
	}
}
