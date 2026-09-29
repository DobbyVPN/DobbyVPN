//go:build linux
// +build linux

package routing

import (
	"errors"
	"fmt"
	"net"
	"sort"

	"core/log"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const (
	linuxOwnedRouteProtocol = 233
	linuxOwnedProxyMetric   = 233
)

var (
	linuxRouteList = func(family int) ([]netlink.Route, error) {
		return netlink.RouteListFiltered(
			family,
			&netlink.Route{Table: unix.RT_TABLE_UNSPEC},
			netlink.RT_FILTER_TABLE,
		)
	}
	linuxRouteAdd     = netlink.RouteAdd
	linuxRouteReplace = netlink.RouteReplace
	linuxRouteDel     = netlink.RouteDel
	linuxRuleList     = netlink.RuleList
	linuxRuleAdd      = netlink.RuleAdd
	linuxRuleDel      = netlink.RuleDel
	linuxLinkByName   = netlink.LinkByName
	linuxLinkByIndex  = netlink.LinkByIndex
)

func GetDefaultInterfaceNameLinux(gatewayIP string) (string, error) {
	log.Debugf(Category, "[Routing][Detect] Looking for default interface via gateway=%s", gatewayIP)

	gateway := net.ParseIP(gatewayIP).To4()
	if gateway == nil {
		err := fmt.Errorf("invalid IPv4 gateway %q", gatewayIP)
		log.Debugf(Category, "[Routing][Detect][ERROR] %v", err)
		return "", err
	}

	routes, err := linuxMainIPv4DefaultRoutes()
	if err != nil {
		log.Debugf(Category, "[Routing][Detect][ERROR] RouteList failed: %v", err)
		return "", fmt.Errorf("failed to list main-table routes: %w", err)
	}
	for _, route := range routes {
		if route.Gw == nil || route.Gw.To4() == nil || !route.Gw.Equal(gateway) || route.LinkIndex <= 0 {
			continue
		}
		link, err := linuxLinkByIndex(route.LinkIndex)
		if err != nil {
			log.Debugf(Category, "[Routing][Detect][ERROR] LinkByIndex(%d) failed: %v", route.LinkIndex, err)
			return "", fmt.Errorf("failed to get link by index %d: %w", route.LinkIndex, err)
		}
		iface := link.Attrs().Name
		log.Debugf(Category, "[Routing][Detect][OK] Found interface=%s for gateway=%s", iface, gatewayIP)
		return iface, nil
	}

	err = fmt.Errorf("default interface for gateway %s not found", gatewayIP)
	log.Debugf(Category, "[Routing][Detect][ERROR] %v", err)
	return "", err
}

// DiscoverLinuxDefaultRoute returns the physical IPv4 gateway and interface
// from the main table. During an active VPN session the ordinary default route
// may point at the TUN device, so gateway-less defaults are ignored.
func DiscoverLinuxDefaultRoute() (gatewayIP, iface string, err error) {
	routes, err := linuxMainIPv4DefaultRoutes()
	if err != nil {
		return "", "", fmt.Errorf("discover main-table default route: %w", err)
	}
	for _, route := range routes {
		if route.Gw == nil || route.Gw.To4() == nil || route.LinkIndex <= 0 {
			continue
		}
		link, linkErr := linuxLinkByIndex(route.LinkIndex)
		if linkErr != nil {
			return "", "", fmt.Errorf("discover main-table default route on link %d: %w", route.LinkIndex, linkErr)
		}
		return route.Gw.To4().String(), link.Attrs().Name, nil
	}
	return "", "", fmt.Errorf("main IPv4 table has no gateway-backed default route")
}

func linuxMainIPv4DefaultRoutes() ([]netlink.Route, error) {
	routes, err := linuxRouteList(netlink.FAMILY_V4)
	if err != nil {
		return nil, err
	}
	defaults := make([]netlink.Route, 0, len(routes))
	for _, route := range routes {
		if route.Table == unix.RT_TABLE_MAIN && linuxRouteIsDefault(route, netlink.FAMILY_V4) {
			defaults = append(defaults, route)
		}
	}
	// RouteList does not promise the same display ordering as `ip route show`.
	// Prefer the lowest metric when several default routes exist.
	sort.SliceStable(defaults, func(i, j int) bool {
		return defaults[i].Priority < defaults[j].Priority
	})
	return defaults, nil
}

func linuxRouteIsDefault(route netlink.Route, family int) bool {
	if route.Dst == nil {
		return true
	}
	ones, bits := route.Dst.Mask.Size()
	if ones != 0 {
		return false
	}
	switch family {
	case netlink.FAMILY_V4:
		return bits == net.IPv4len*8 && route.Dst.IP.To4() != nil
	case netlink.FAMILY_V6:
		return bits == net.IPv6len*8 && route.Dst.IP.To4() == nil && route.Dst.IP.To16() != nil
	default:
		return false
	}
}

func linuxDefaultIPNet(family int) *net.IPNet {
	switch family {
	case netlink.FAMILY_V4:
		return &net.IPNet{IP: net.IPv4zero, Mask: net.CIDRMask(0, net.IPv4len*8)}
	case netlink.FAMILY_V6:
		return &net.IPNet{IP: net.IPv6zero, Mask: net.CIDRMask(0, net.IPv6len*8)}
	default:
		return nil
	}
}

func linuxIPv4HostRoute(ip string) (*net.IPNet, error) {
	parsed := net.ParseIP(ip).To4()
	if parsed == nil {
		return nil, fmt.Errorf("invalid IPv4 address %q", ip)
	}
	return &net.IPNet{IP: parsed, Mask: net.CIDRMask(32, net.IPv4len*8)}, nil
}

func linuxLinkIndex(name string) (int, error) {
	if name == "" {
		return 0, fmt.Errorf("empty Linux interface name")
	}
	link, err := linuxLinkByName(name)
	if err != nil {
		return 0, fmt.Errorf("find Linux interface %q: %w", name, err)
	}
	if index := link.Attrs().Index; index > 0 {
		return index, nil
	}
	return 0, fmt.Errorf("Linux interface %q has invalid index %d", name, link.Attrs().Index)
}

func linuxAlreadyExists(err error) bool {
	return errors.Is(err, unix.EEXIST)
}

func linuxRouteAlreadyGone(err error) bool {
	return errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENODEV)
}

func linuxRouteOperation(action string, route *netlink.Route, operation func(*netlink.Route) error) error {
	err := operation(route)
	if err != nil {
		log.Debugf(Category, "[Routing][Netlink][ERROR] route %s=%+v err=%v", action, *route, err)
		return fmt.Errorf("route %s %+v: %w", action, *route, err)
	}
	log.Debugf(Category, "[Routing][Netlink][OK] route %s=%+v", action, *route)
	return nil
}

func linuxRuleOperation(action string, rule *netlink.Rule, operation func(*netlink.Rule) error) error {
	err := operation(rule)
	if err != nil {
		log.Debugf(Category, "[Routing][Netlink][ERROR] rule %s=%+v err=%v", action, *rule, err)
		return fmt.Errorf("rule %s %+v: %w", action, *rule, err)
	}
	log.Debugf(Category, "[Routing][Netlink][OK] rule %s=%+v", action, *rule)
	return nil
}

func ReconcileLinuxSessionRoutesWithRule(proxyIP, gatewayIP, iface string, tableID, priority int) error {
	if tableID <= 0 || uint64(tableID) > uint64(^uint32(0)) {
		return fmt.Errorf("invalid Linux routing table %d", tableID)
	}
	if priority > 0 && uint64(priority) > uint64(^uint32(0)) {
		return fmt.Errorf("invalid Linux routing rule priority %d", priority)
	}
	if !isLoopbackIP(proxyIP) {
		route, err := linuxGatewayRoute(proxyIP, gatewayIP, iface, unix.RT_TABLE_MAIN, linuxOwnedRouteProtocol, linuxOwnedProxyMetric)
		if err != nil {
			return fmt.Errorf("restore proxy route: %w", err)
		}
		if err := linuxRouteOperation("replace", &route, linuxRouteReplace); err != nil {
			return fmt.Errorf("restore proxy route %s via %s dev %s: %w", proxyIP, gatewayIP, iface, err)
		}
	}
	route, err := linuxGatewayRoute("", gatewayIP, iface, tableID, linuxOwnedRouteProtocol, 0)
	if err != nil {
		return fmt.Errorf("restore marked default route: %w", err)
	}
	if err := linuxRouteOperation("replace", &route, linuxRouteReplace); err != nil {
		return fmt.Errorf("restore marked default route table=%d via %s dev %s: %w", tableID, gatewayIP, iface, err)
	}
	if priority <= 0 {
		return nil
	}
	if err := linuxAddSessionRule(tableID, priority); err != nil {
		return fmt.Errorf("restore fwmark rule table=%d priority=%d: %w", tableID, priority, err)
	}
	return nil
}

func linuxGatewayRoute(destination, gatewayIP, iface string, tableID, protocol, metric int) (netlink.Route, error) {
	gateway := net.ParseIP(gatewayIP).To4()
	if gateway == nil {
		return netlink.Route{}, fmt.Errorf("invalid IPv4 gateway %q", gatewayIP)
	}
	linkIndex, err := linuxLinkIndex(iface)
	if err != nil {
		return netlink.Route{}, err
	}
	dst := linuxDefaultIPNet(netlink.FAMILY_V4)
	if destination != "" {
		dst, err = linuxIPv4HostRoute(destination)
		if err != nil {
			return netlink.Route{}, err
		}
	}
	return netlink.Route{
		Dst:       dst,
		Gw:        gateway,
		LinkIndex: linkIndex,
		Protocol:  netlink.RouteProtocol(protocol),
		Priority:  metric,
		Family:    netlink.FAMILY_V4,
		Table:     tableID,
		Type:      unix.RTN_UNICAST,
		Scope:     netlink.SCOPE_UNIVERSE,
	}, nil
}

func linuxSessionRule(tableID, priority int) *netlink.Rule {
	rule := netlink.NewRule()
	rule.Family = netlink.FAMILY_V4
	rule.Table = tableID
	rule.Mark = uint32(tableID)
	rule.Priority = priority
	return rule
}

func linuxSessionRuleMatches(rule netlink.Rule, tableID, priority int) bool {
	if rule.Family != netlink.FAMILY_V4 || rule.Priority != priority || rule.Table != tableID ||
		rule.Mark != uint32(tableID) || rule.Src != nil || rule.Dst != nil || rule.Tos != 0 ||
		rule.Goto != -1 || rule.Flow != -1 || rule.IifName != "" || rule.OifName != "" ||
		rule.SuppressIfgroup != -1 || rule.SuppressPrefixlen != -1 || rule.Invert ||
		rule.Dport != nil || rule.Sport != nil || rule.UIDRange != nil || rule.IPProto != 0 ||
		rule.TunID != 0 || rule.Protocol != 0 || rule.Type != 0 {
		return false
	}
	return rule.Mask == nil || *rule.Mask == ^uint32(0)
}

func linuxAddSessionRule(tableID, priority int) error {
	exists, err := linuxSessionRuleExists(tableID, priority)
	if err != nil {
		return fmt.Errorf("inspect existing fwmark rules: %w", err)
	}
	if exists {
		return nil
	}
	rule := linuxSessionRule(tableID, priority)
	if err := linuxRuleOperation("add", rule, linuxRuleAdd); err == nil {
		return nil
	} else if !linuxAlreadyExists(err) {
		return err
	}
	exists, err = linuxSessionRuleExists(tableID, priority)
	if err != nil {
		return fmt.Errorf("rule already exists but verification failed: %w", err)
	}
	if exists {
		return nil
	}
	return fmt.Errorf("rule priority %d already exists with different selectors", priority)
}

func linuxSessionRuleExists(tableID, priority int) (bool, error) {
	rules, err := linuxRuleList(netlink.FAMILY_V4)
	if err != nil {
		return false, err
	}
	for _, existing := range rules {
		if linuxSessionRuleMatches(existing, tableID, priority) {
			return true, nil
		}
	}
	return false, nil
}

func isLoopbackIP(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.IsLoopback()
}
