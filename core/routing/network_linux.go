//go:build linux && !android

package routing

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"sort"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// LinuxPhysicalInterface reads the unchanged physical main table. Both address
// families are eligible, including gateway-less point-to-point defaults.
func LinuxPhysicalInterface(tunName string) (int, error) {
	for _, family := range []int{netlink.FAMILY_V4, netlink.FAMILY_V6} {
		routes, err := linuxRouteList(family)
		if err != nil {
			return 0, err
		}
		sort.SliceStable(routes, func(i, j int) bool { return routes[i].Priority < routes[j].Priority })
		for _, route := range routes {
			if route.Table != unix.RT_TABLE_MAIN || route.Type != unix.RTN_UNICAST || !linuxRouteIsDefault(route, family) || route.LinkIndex <= 0 {
				continue
			}
			link, err := linuxLinkByIndex(route.LinkIndex)
			if err != nil {
				return 0, err
			}
			if link.Attrs().Name != tunName && link.Attrs().Flags&net.FlagUp != 0 {
				return route.LinkIndex, nil
			}
		}
	}
	return 0, errors.New("no physical IPv4 or IPv6 default route")
}

// AcquireLinuxNetwork routes application traffic through one retained TUN.
// Marked transport/bootstrap/bypass sockets use the physical main table; the
// terminal mark rule prevents them falling back into the VPN when a link dies.
// No physical route is deleted or restored, including after a network change.
func (p *Plan) AcquireLinuxNetwork(tunName string, tableID, priority int) error {
	if tableID <= 0 || uint64(tableID) > math.MaxUint32 || tableID >= unix.RT_TABLE_DEFAULT && tableID <= unix.RT_TABLE_LOCAL || priority <= 0 || uint64(priority) > math.MaxUint32-2 {
		return errors.New("invalid owned Linux policy routing identity")
	}
	index, err := linuxLinkIndex(tunName)
	if err != nil {
		return err
	}
	mark := uint32(tableID)
	for _, family := range []int{netlink.FAMILY_V4, netlink.FAMILY_V6} {
		if err := p.acquireLinuxNetworkFamily(tableID, priority, index, mark, family); err != nil {
			return err
		}
	}
	return nil
}

func (p *Plan) acquireLinuxNetworkFamily(tableID, priority, index int, mark uint32, family int) error {
	routes, err := linuxRouteList(family)
	if err != nil {
		return err
	}
	for _, route := range routes {
		if route.Table == tableID {
			return fmt.Errorf("routing table %d is already in use", tableID)
		}
	}
	rules, err := linuxRuleList(family)
	if err != nil {
		return err
	}
	for _, rule := range rules {
		if rule.Priority >= priority && rule.Priority <= priority+2 {
			return fmt.Errorf("routing priority %d is already in use", rule.Priority)
		}
	}

	route := netlink.Route{Dst: linuxDefaultIPNet(family), LinkIndex: index, Protocol: linuxOwnedRouteProtocol,
		Family: family, Table: tableID, Type: unix.RTN_UNICAST, Scope: netlink.SCOPE_LINK}
	if _, routeAcquireErr := p.acquireLinuxNetworkRoute(route); routeAcquireErr != nil {
		return routeAcquireErr
	}
	for offset := 0; offset < 3; offset++ {
		rule := netlink.NewRule()
		rule.Family, rule.Priority = family, priority+offset
		rule.Protocol = uint8(linuxOwnedRouteProtocol)
		rule.Type = unix.FR_ACT_TO_TBL
		switch offset {
		case 0:
			rule.Mark, rule.Table = mark, unix.RT_TABLE_MAIN
		case 1:
			rule.Mark, rule.Table, rule.Type = mark, 0, unix.FR_ACT_UNREACHABLE
		case 2:
			rule.Table = tableID
		}
		if _, acquireErr := p.Acquire(fmt.Sprintf("network-rule family=%d priority=%d", family, rule.Priority), func() error {
			return linuxRuleOperation("add", rule, linuxRuleAdd)
		}, func(context.Context) error {
			cleanupErr := linuxRuleOperation("delete", rule, linuxRuleDel)
			if linuxRouteAlreadyGone(cleanupErr) {
				return nil
			}
			return cleanupErr
		}); acquireErr != nil {
			return acquireErr
		}
	}
	return nil
}

func RecoverLinuxNetworkRules(tableID, priority int, tunName string) error {
	if tableID <= 0 || uint64(tableID) > math.MaxUint32 || tableID >= unix.RT_TABLE_DEFAULT && tableID <= unix.RT_TABLE_LOCAL || priority <= 0 || uint64(priority) > math.MaxUint32-2 {
		return errors.New("invalid owned Linux policy routing identity")
	}
	mark := uint32(tableID)
	for _, family := range []int{netlink.FAMILY_V4, netlink.FAMILY_V6} {
		if err := recoverLinuxNetworkFamily(family, tableID, priority, mark, tunName); err != nil {
			return err
		}
	}
	return nil
}

func recoverLinuxNetworkFamily(family, tableID, priority int, mark uint32, tunName string) error {
	if err := recoverLinuxNetworkFamilyRules(family, priority, tableID, mark); err != nil {
		return err
	}
	return recoverLinuxNetworkFamilyRoutes(family, tableID, tunName)
}

func recoverLinuxNetworkFamilyRules(family, priority, tableID int, mark uint32) error {
	rules, err := linuxRuleList(family)
	if err != nil {
		return err
	}
	for _, rule := range rules {
		if !linuxOwnsNetworkRule(rule, priority, tableID, mark) {
			continue
		}
		if deleteErr := linuxRuleOperation("delete", &rule, linuxRuleDel); deleteErr != nil && !linuxRouteAlreadyGone(deleteErr) {
			return deleteErr
		}
	}
	return nil
}

func linuxOwnsNetworkRule(rule netlink.Rule, priority, tableID int, mark uint32) bool {
	if rule.Protocol != uint8(linuxOwnedRouteProtocol) || rule.Priority < priority || rule.Priority > priority+2 {
		return false
	}
	selectorRule := rule
	selectorRule.Protocol = 0
	if !linuxSessionRuleHasNoAddressSelectors(selectorRule) || !linuxSessionRuleHasNoFlowSelectors(selectorRule) || !linuxSessionRuleHasFullMarkMask(selectorRule) {
		return false
	}
	switch rule.Priority {
	case priority:
		return rule.Mark == mark && rule.Table == unix.RT_TABLE_MAIN && rule.Type == unix.FR_ACT_TO_TBL
	case priority + 1:
		return rule.Mark == mark && rule.Table == 0 && rule.Type == unix.FR_ACT_UNREACHABLE
	case priority + 2:
		return rule.Mark == 0 && rule.Table == tableID && rule.Type == unix.FR_ACT_TO_TBL
	default:
		return false
	}
}

func recoverLinuxNetworkFamilyRoutes(family, tableID int, tunName string) error {
	routes, err := linuxRouteList(family)
	if err != nil {
		return err
	}
	for _, route := range routes {
		if route.Table != tableID || route.Protocol != linuxOwnedRouteProtocol || route.Type != unix.RTN_UNICAST || route.Gw != nil || !linuxRouteIsDefault(route, family) || route.LinkIndex <= 0 {
			continue
		}
		link, linkErr := linuxLinkByIndex(route.LinkIndex)
		if linkErr != nil {
			return linkErr
		}
		if link.Attrs().Name != tunName {
			continue
		}
		if deleteErr := linuxRouteOperation("delete", &route, linuxRouteDel); deleteErr != nil && !linuxRouteAlreadyGone(deleteErr) {
			return deleteErr
		}
	}
	return nil
}

func (p *Plan) acquireLinuxNetworkRoute(route netlink.Route) (*Lease, error) {
	return p.Acquire(fmt.Sprintf("network-route %s table=%d", route.Dst, route.Table), func() error {
		return linuxRouteOperation("add", &route, linuxRouteAdd)
	}, func(context.Context) error {
		err := linuxRouteOperation("delete", &route, linuxRouteDel)
		if linuxRouteAlreadyGone(err) {
			return nil
		}
		return err
	})
}
