//go:build linux && !(android || ios)

package routing

import (
	"errors"
	"fmt"
	"net"

	"core/log"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

type linuxRecoveryRoutes struct {
	deletes                 []netlink.Route
	hasIndependentDefault   bool
	restoreDefault          *netlink.Route
	restoreDefaultAmbiguous bool
}

func linuxOwnedProxyRoute(route netlink.Route) bool {
	if route.Table != unix.RT_TABLE_MAIN || route.Family != netlink.FAMILY_V4 ||
		route.Protocol != linuxOwnedRouteProtocol || route.Priority != linuxOwnedProxyMetric ||
		route.Type != unix.RTN_UNICAST || route.Gw == nil || route.Gw.To4() == nil ||
		route.LinkIndex <= 0 || len(route.MultiPath) != 0 || route.Dst == nil {
		return false
	}
	ones, bits := route.Dst.Mask.Size()
	return bits == net.IPv4len*8 && ones == 32 && route.Dst.IP.To4() != nil
}

func linuxOwnedTunnelRoute(route netlink.Route, tunName string) bool {
	if route.Table != unix.RT_TABLE_MAIN || route.Family != netlink.FAMILY_V4 ||
		!linuxRouteIsDefault(route, netlink.FAMILY_V4) ||
		route.Protocol != linuxOwnedRouteProtocol || route.Type != unix.RTN_UNICAST ||
		route.LinkIndex <= 0 || tunName == "" {
		return false
	}
	link, err := linuxLinkByIndex(route.LinkIndex)
	if err != nil {
		log.Warnf(Category, "[Linux][Recovery] cannot identify route link index=%d: %v", route.LinkIndex, err)
		return false
	}
	return link.Attrs().Name == tunName
}

func linuxOwnedMarkedGatewayRoute(route netlink.Route, tableID int) bool {
	return route.Table == tableID && route.Family == netlink.FAMILY_V4 &&
		linuxRouteIsDefault(route, netlink.FAMILY_V4) &&
		route.Protocol == linuxOwnedRouteProtocol && route.Type == unix.RTN_UNICAST &&
		route.Gw != nil && route.Gw.To4() != nil && route.LinkIndex > 0 && len(route.MultiPath) == 0
}

func linuxOwnedTerminalRoute(route netlink.Route, tableID int) bool {
	return route.Table == tableID && route.Family == netlink.FAMILY_V4 &&
		linuxRouteIsDefault(route, netlink.FAMILY_V4) && route.Type == unix.RTN_UNREACHABLE &&
		route.Protocol == linuxOwnedRouteProtocol && route.Priority == 1 &&
		route.Gw == nil && route.LinkIndex == 0
}

func linuxOwnedIPv6Route(route netlink.Route) bool {
	if route.Table != unix.RT_TABLE_MAIN || route.Family != netlink.FAMILY_V6 ||
		route.Type != unix.RTN_BLACKHOLE || route.Protocol != linuxOwnedRouteProtocol || route.Priority != 1 ||
		route.Dst == nil {
		return false
	}
	return route.Dst.String() == ipv6LowerHalf || route.Dst.String() == ipv6UpperHalf
}

func linuxRecoveryRouteForDelete(route netlink.Route, family int) netlink.Route {
	if route.Dst == nil && linuxRouteIsDefault(route, family) {
		route.Dst = linuxDefaultIPNet(family)
	}
	return route
}

func collectLinuxRecoveryRoutes(ipv4Routes, ipv6Routes []netlink.Route, tableID int, tunName string) linuxRecoveryRoutes {
	var recovered linuxRecoveryRoutes
	for _, route := range ipv4Routes {
		if route.Table == unix.RT_TABLE_MAIN {
			if linuxOwnedProxyRoute(route) {
				recovered.deletes = append(recovered.deletes, route)
				continue
			}
			if linuxOwnedTunnelRoute(route, tunName) {
				recovered.deletes = append(recovered.deletes, linuxRecoveryRouteForDelete(route, netlink.FAMILY_V4))
				continue
			}
			if linuxRouteIsDefault(route, netlink.FAMILY_V4) {
				recovered.hasIndependentDefault = true
			}
			continue
		}
		if route.Table != tableID {
			continue
		}
		if linuxOwnedTerminalRoute(route, tableID) {
			recovered.deletes = append(recovered.deletes, linuxRecoveryRouteForDelete(route, netlink.FAMILY_V4))
			continue
		}
		if !linuxOwnedMarkedGatewayRoute(route, tableID) {
			continue
		}
		recovered.deletes = append(recovered.deletes, linuxRecoveryRouteForDelete(route, netlink.FAMILY_V4))
		candidate := netlink.Route{
			Dst:       linuxDefaultIPNet(netlink.FAMILY_V4),
			Gw:        append(net.IP(nil), route.Gw.To4()...),
			LinkIndex: route.LinkIndex,
			Protocol:  unix.RTPROT_BOOT,
			Family:    netlink.FAMILY_V4,
			Table:     unix.RT_TABLE_MAIN,
			Type:      unix.RTN_UNICAST,
			Scope:     netlink.SCOPE_UNIVERSE,
		}
		if recovered.restoreDefault == nil {
			recovered.restoreDefault = &candidate
		} else if recovered.restoreDefault.LinkIndex != candidate.LinkIndex ||
			!recovered.restoreDefault.Gw.Equal(candidate.Gw) {
			recovered.restoreDefaultAmbiguous = true
		}
	}
	for _, route := range ipv6Routes {
		if linuxOwnedIPv6Route(route) {
			recovered.deletes = append(recovered.deletes, route)
		}
	}
	return recovered
}

// RecoverLinuxOwnedRoutes removes only typed routes carrying DobbyVPN's
// explicit protocol/metric ownership tags and the exact fwmark rule used by
// this session. If process death removed the TUN and its main-table default,
// the marked table supplies the uplink gateway and interface for restoration.
func RecoverLinuxOwnedRoutes(tableID, priority int, tunName string) error {
	mainRoutes, err := linuxRouteList(netlink.FAMILY_V4)
	if err != nil {
		return fmt.Errorf("inspect owned IPv4 routes: %w", err)
	}
	ipv6Routes, err := linuxRouteList(netlink.FAMILY_V6)
	if err != nil {
		return fmt.Errorf("inspect owned IPv6 routes: %w", err)
	}
	routes := collectLinuxRecoveryRoutes(mainRoutes, ipv6Routes, tableID, tunName)

	var ownedRules []netlink.Rule
	rules, ruleListErr := linuxRuleList(netlink.FAMILY_V4)
	if ruleListErr == nil {
		for _, rule := range rules {
			if linuxSessionRuleMatches(rule, tableID, priority) {
				ownedRules = append(ownedRules, rule)
			}
		}
	}
	if len(routes.deletes) == 0 && len(ownedRules) == 0 && ruleListErr == nil {
		return nil
	}

	log.Debugf(Category, "[Linux][Recovery] removing %d tagged route(s) and %d owned rule(s) after process loss", len(routes.deletes), len(ownedRules))
	var errs []error
	if ruleListErr != nil {
		errs = append(errs, fmt.Errorf("inspect owned fwmark rule: %w", ruleListErr))
	}
	for index := range ownedRules {
		rule := ownedRules[index]
		if deleteErr := linuxRuleOperation("delete", &rule, linuxRuleDel); deleteErr != nil && !linuxRouteAlreadyGone(deleteErr) {
			errs = append(errs, fmt.Errorf("remove owned fwmark rule: %w", deleteErr))
		}
	}
	for _, route := range routes.deletes {
		if deleteErr := linuxRouteOperation("delete", &route, linuxRouteDel); deleteErr != nil && !linuxRouteAlreadyGone(deleteErr) {
			errs = append(errs, fmt.Errorf("remove owned route %s: %w", route.String(), deleteErr))
		}
	}
	if routes.restoreDefaultAmbiguous {
		errs = append(errs, fmt.Errorf("restore interrupted default route: owned marked routes disagree"))
	} else if routes.restoreDefault != nil && !routes.hasIndependentDefault {
		if restoreErr := linuxRouteOperation("replace", routes.restoreDefault, linuxRouteReplace); restoreErr != nil {
			errs = append(errs, fmt.Errorf("restore interrupted default route: %w", restoreErr))
		}
	}
	return errors.Join(errs...)
}
