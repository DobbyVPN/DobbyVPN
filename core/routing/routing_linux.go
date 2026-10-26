//go:build linux && !android

package routing

import (
	"errors"
	"fmt"
	"net"

	"core/log"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const linuxOwnedRouteProtocol netlink.RouteProtocol = 233

const linuxOwnedProxyMetric = 233

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
		return &net.IPNet{IP: net.IPv4zero.To4(), Mask: net.CIDRMask(0, net.IPv4len*8)}
	case netlink.FAMILY_V6:
		return &net.IPNet{IP: net.IPv6zero, Mask: net.CIDRMask(0, net.IPv6len*8)}
	default:
		return nil
	}
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
	return 0, fmt.Errorf("linux interface %q has invalid index %d", name, link.Attrs().Index)
}

func linuxRouteAlreadyGone(err error) bool {
	return errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENODEV)
}

func linuxRouteOperation(action string, route *netlink.Route, operation func(*netlink.Route) error) error {
	err := operation(route)
	if err != nil {
		if action == "delete" && linuxRouteAlreadyGone(err) {
			log.Debugf(Category, "[Routing][Netlink] route already absent: %+v err=%v", *route, err)
			return err
		}
		log.Errorf(Category, "[Routing][Netlink] route %s=%+v err=%v", action, *route, err)
		return fmt.Errorf("route %s %+v: %w", action, *route, err)
	}
	log.Debugf(Category, "[Routing][Netlink][OK] route %s=%+v", action, *route)
	return nil
}

func linuxRuleOperation(action string, rule *netlink.Rule, operation func(*netlink.Rule) error) error {
	err := operation(rule)
	if err != nil {
		if action == "delete" && linuxRouteAlreadyGone(err) {
			log.Debugf(Category, "[Routing][Netlink] rule already absent: %+v err=%v", *rule, err)
			return err
		}
		log.Errorf(Category, "[Routing][Netlink] rule %s=%+v err=%v", action, *rule, err)
		return fmt.Errorf("rule %s %+v: %w", action, *rule, err)
	}
	log.Debugf(Category, "[Routing][Netlink][OK] rule %s=%+v", action, *rule)
	return nil
}

func linuxSessionRuleMatches(rule netlink.Rule, tableID, priority int) bool {
	mark, valid := linuxSessionTableMark(tableID)
	return valid && rule.Family == netlink.FAMILY_V4 && rule.Priority == priority &&
		rule.Table == tableID && rule.Mark == mark && linuxSessionRuleHasNoAddressSelectors(rule) &&
		linuxSessionRuleHasNoFlowSelectors(rule) && linuxSessionRuleHasNoTransportSelectors(rule) &&
		linuxSessionRuleHasFullMarkMask(rule)
}

func linuxSessionTableMark(tableID int) (uint32, bool) {
	if tableID <= 0 || uint64(tableID) > uint64(^uint32(0)) {
		return 0, false
	}
	return uint32(tableID), true
}

func linuxSessionRuleHasNoAddressSelectors(rule netlink.Rule) bool {
	return rule.Src == nil && rule.Dst == nil && rule.Tos == 0 && !rule.Invert
}

func linuxSessionRuleHasNoFlowSelectors(rule netlink.Rule) bool {
	return rule.Goto == -1 && rule.Flow == -1 && rule.IifName == "" && rule.OifName == "" &&
		rule.SuppressIfgroup == -1 && rule.SuppressPrefixlen == -1
}

func linuxSessionRuleHasNoTransportSelectors(rule netlink.Rule) bool {
	return rule.Dport == nil && rule.Sport == nil && rule.UIDRange == nil && rule.IPProto == 0 &&
		rule.TunID == 0 && rule.Protocol == 0 && rule.Type == 0
}

func linuxSessionRuleHasFullMarkMask(rule netlink.Rule) bool {
	return rule.Mask == nil || *rule.Mask == ^uint32(0)
}
