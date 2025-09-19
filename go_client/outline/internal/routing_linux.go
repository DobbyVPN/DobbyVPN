//go:build linux
// +build linux

package internal

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/vishvananda/netlink"
)

var ipRules []*netlink.Rule

func startRouting(proxyIP string, config *RoutingConfig) error {
	if err := setupRoutingTable(config.RoutingTableID, config.TunDeviceName, config.TunGatewayCIDR, config.TunDeviceIP); err != nil {
		return err
	}

	// исключения для IP rule
	exclusions := []string{}

	// если proxyIP валиден и не loopback, исключаем его
	if ip := net.ParseIP(proxyIP); ip != nil && !ip.IsLoopback() {
		exclusions = append(exclusions, ip.String()+"/32")
	}

	// временно захардкоженный сервер
	exclusions = append(exclusions, "85.9.223.19/32")
	exclusions = append(exclusions, "127.0.0.1/32")

	for _, cidr := range exclusions {
		if err := setupIpRule(cidr, config.RoutingTableID, config.RoutingTablePriority); err != nil {
			return err
		}
	}

	return nil
}

func stopRouting(routingTable int) {
	if err := cleanUpRoutingTable(routingTable); err != nil {
		logging.Err.Printf("failed to clean up routing table '%v': %v\n", routingTable, err)
	}
	if err := cleanUpRules(); err != nil {
		logging.Err.Printf("failed to clean up IP rules: %v\n", err)
	}
}

func setupRoutingTable(routingTable int, tunName, gwSubnet string, tunIP string) error {
	tun, err := netlink.LinkByName(tunName)
	if err != nil {
		return fmt.Errorf("failed to find tun device '%s': %w", tunName, err)
	}

	dst, err := netlink.ParseIPNet(gwSubnet)
	if err != nil {
		return fmt.Errorf("failed to parse gateway '%s': %w", gwSubnet, err)
	}

	src := net.ParseIP(tunIP)
	if src == nil {
		return fmt.Errorf("invalid tun IP '%s'", tunIP)
	}

	// маршрут для подсети туннеля
	r := netlink.Route{
		LinkIndex: tun.Attrs().Index,
		Table:     routingTable,
		Dst:       dst,
		Src:       src,
		Scope:     netlink.SCOPE_LINK,
	}
	if err = netlink.RouteReplace(&r); err != nil {
		return fmt.Errorf("failed to add/replace routing entry '%v' -> '%v': %w", r.Src, r.Dst, err)
	}
	logging.Info.Printf("routing traffic from %v to %v through nic %v\n", r.Src, r.Dst, r.LinkIndex)

	// дефолтный маршрут через gw
	r = netlink.Route{
		LinkIndex: tun.Attrs().Index,
		Table:     routingTable,
		Gw:        dst.IP,
	}
	if err := netlink.RouteReplace(&r); err != nil {
		return fmt.Errorf("failed to add/replace gateway routing entry '%v': %w", r.Gw, err)
	}
	logging.Info.Printf("routing traffic via gw %v through nic %v...\n", r.Gw, r.LinkIndex)

	return nil
}

func cleanUpRoutingTable(routingTable int) error {
	filter := netlink.Route{Table: routingTable}
	routes, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &filter, netlink.RT_FILTER_TABLE)
	if err != nil {
		return fmt.Errorf("failed to list entries in routing table '%v': %w", routingTable, err)
	}

	var rtDelErr error
	for _, route := range routes {
		if err := netlink.RouteDel(&route); err != nil {
			rtDelErr = errors.Join(rtDelErr, fmt.Errorf("failed to remove routing entry: %w", err))
		}
	}
	if rtDelErr == nil {
		logging.Info.Printf("routing table '%v' has been cleaned up\n", routingTable)
	}
	return rtDelErr
}

func setupIpRule(cidr string, routingTable, routingPriority int) error {
	dst, err := netlink.ParseIPNet(cidr)
	if err != nil {
		return fmt.Errorf("failed to parse server IP CIDR '%s': %w", cidr, err)
	}

	// Проверка: есть ли уже такое правило
	rules, _ := netlink.RuleList(netlink.FAMILY_V4)
	for _, r := range rules {
		if r.Table == routingTable && r.Priority == routingPriority &&
			r.Invert && ipNetEqual(r.Dst, dst) {
			logging.Info.Printf("ip rule already exists for %v via table %v\n", dst, routingTable)
			return nil
		}
	}

	rule := netlink.NewRule()
	rule.Priority = routingPriority
	rule.Family = netlink.FAMILY_V4
	rule.Table = routingTable
	rule.Dst = dst
	rule.Invert = true

	if err := netlink.RuleAdd(rule); err != nil && !strings.Contains(err.Error(), "file exists") {
		return fmt.Errorf("failed to add IP rule (table %v, dst %v): %w", rule.Table, rule.Dst, err)
	}
	logging.Info.Printf("ip rule 'from all not to %v via table %v' created\n", rule.Dst, rule.Table)
	ipRules = append(ipRules, rule)
	return nil
}

func cleanUpRules() error {
	var delErr error
	for _, rule := range ipRules {
		if err := netlink.RuleDel(rule); err != nil {
			delErr = errors.Join(delErr, fmt.Errorf("failed to delete IP rule (table %v dst %v): %w", rule.Table, rule.Dst, err))
		}
	}
	ipRules = nil
	return delErr
}

func ipNetEqual(a, b *net.IPNet) bool {
	if a == nil && b == nil {
		return true
	}
	if (a == nil) != (b == nil) {
		return false
	}
	return a.IP.Equal(b.IP) && a.Mask.String() == b.Mask.String()
}
