//go:build linux && !(android || ios)

package routing

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"strings"

	"core/sessionapi"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// AcquireLinuxProxyRoute installs a host bypass only when this session added
// it. Existing routes are left untouched and therefore are never removed by
// the lease.
func (p *Plan) AcquireLinuxProxyRoute(proxyIP, gatewayIP, iface string) (*Lease, error) {
	if isLoopbackIP(proxyIP) {
		return nil, nil
	}
	route, err := linuxGatewayRoute(proxyIP, gatewayIP, iface, unix.RT_TABLE_MAIN, linuxOwnedProxyMetric)
	if err != nil {
		return nil, err
	}
	var created bool
	return p.Acquire("proxy-route "+proxyIP, func() error {
		if err := linuxRouteOperation("add", &route, linuxRouteAdd); err != nil {
			if linuxAlreadyExists(err) {
				return nil
			}
			return err
		}
		created = true
		return nil
	}, func(_ context.Context) error {
		if !created {
			return nil
		}
		err := linuxRouteOperation("delete", &route, linuxRouteDel)
		if linuxRouteAlreadyGone(err) {
			return nil
		}
		return err
	})
}

// AcquireLinuxMarkedRouting installs an exact table default and fwmark rule.
// Unlike the legacy helper it never deletes or replaces another session's
// rule. If either resource already exists, acquisition fails rather than
// claiming ownership of it.
func (p *Plan) AcquireLinuxMarkedRouting(tableID, priority int, iface, gatewayIP string) error {
	if tableID <= 0 || uint64(tableID) > uint64(^uint32(0)) {
		return fmt.Errorf("invalid Linux routing table %d", tableID)
	}
	if priority <= 0 || uint64(priority) > uint64(^uint32(0)) {
		return fmt.Errorf("invalid Linux routing rule priority %d", priority)
	}
	existingRule, err := linuxSessionRuleExists(tableID, priority)
	if err != nil {
		return fmt.Errorf("inspect existing fwmark rule: %w", err)
	}
	if existingRule {
		return fmt.Errorf("fwmark rule table=%d priority=%d already exists", tableID, priority)
	}
	mainRoute, err := linuxGatewayRoute("", gatewayIP, iface, tableID, 0)
	if err != nil {
		return err
	}
	routeLease, err := p.Acquire(fmt.Sprintf("mark-route table=%d", tableID), func() error {
		return linuxRouteOperation("add", &mainRoute, linuxRouteAdd)
	}, func(_ context.Context) error {
		cleanupErr := linuxRouteOperation("delete", &mainRoute, linuxRouteDel)
		if linuxRouteAlreadyGone(cleanupErr) {
			return nil
		}
		return cleanupErr
	})
	if err != nil {
		return err
	}

	// Link loss removes the physical default. Without a terminal route, marked
	// VPN-server traffic falls through to the main TUN route and loops back into
	// the VPN. Metric 1 loses to the physical route's metric 0 and survives link loss.
	terminalRoute := netlink.Route{
		Dst:      linuxDefaultIPNet(netlink.FAMILY_V4),
		Protocol: linuxOwnedRouteProtocol,
		Priority: 1,
		Family:   netlink.FAMILY_V4,
		Table:    tableID,
		Type:     unix.RTN_UNREACHABLE,
	}
	terminalLease, err := p.Acquire(fmt.Sprintf("mark-unreachable table=%d", tableID), func() error {
		return linuxRouteOperation("add", &terminalRoute, linuxRouteAdd)
	}, func(_ context.Context) error {
		routeErr := linuxRouteOperation("delete", &terminalRoute, linuxRouteDel)
		if linuxRouteAlreadyGone(routeErr) {
			return nil
		}
		return routeErr
	})
	if err != nil {
		return errors.Join(err, routeLease.Close(context.Background()))
	}

	if _, err := p.Acquire(fmt.Sprintf("mark-rule table=%d priority=%d", tableID, priority), func() error {
		rule := linuxSessionRule(tableID, priority)
		return linuxRuleOperation("add", rule, linuxRuleAdd)
	}, func(_ context.Context) error {
		rule := linuxSessionRule(tableID, priority)
		return linuxRuleOperation("delete", rule, linuxRuleDel)
	}); err != nil {
		return errors.Join(err, terminalLease.Close(context.Background()), routeLease.Close(context.Background()))
	}
	return nil
}

// AcquireLinuxTunnelDefault replaces the active default route with the TUN
// route and records the exact previous route. Release deletes only the TUN
// default and restores that recorded baseline; it never guesses a gateway.
func (p *Plan) AcquireLinuxTunnelDefault(tunName string) (*Lease, error) {
	var baseline netlink.Route
	var tunRoute netlink.Route
	return p.Acquire("tun-default "+tunName, func() error {
		defaults, err := linuxMainIPv4DefaultRoutes()
		if err != nil {
			return fmt.Errorf("capture main-table default route: %w", err)
		}
		if len(defaults) == 0 {
			return fmt.Errorf("no IPv4 default route to preserve")
		}
		baseline = defaults[0]
		if baseline.Dst == nil {
			baseline.Dst = linuxDefaultIPNet(netlink.FAMILY_V4)
		}
		tunLinkIndex, err := linuxLinkIndex(tunName)
		if err != nil {
			return err
		}
		tunRoute = netlink.Route{
			Dst:       linuxDefaultIPNet(netlink.FAMILY_V4),
			LinkIndex: tunLinkIndex,
			Protocol:  linuxOwnedRouteProtocol,
			Family:    netlink.FAMILY_V4,
			Table:     unix.RT_TABLE_MAIN,
			Type:      unix.RTN_UNICAST,
			Scope:     netlink.SCOPE_LINK,
		}
		if err := linuxRouteOperation("replace", &tunRoute, linuxRouteReplace); err != nil {
			return fmt.Errorf("install TUN default route: %w", err)
		}
		return nil
	}, func(_ context.Context) error {
		// Delete the route owned by this session first. A failed delete must not
		// restore a baseline over a route changed by another actor.
		if err := linuxRouteOperation("delete", &tunRoute, linuxRouteDel); err != nil {
			return err
		}
		if err := linuxRouteOperation("replace", &baseline, linuxRouteReplace); err != nil {
			return fmt.Errorf("restore captured main-table default route: %w", err)
		}
		return nil
	})
}

// AcquireLinuxResolvedDNS makes systemd-resolved send all DNS through this
// session's TUN. The TUN is newly created for the session, so reverting its
// per-link state restores the exact baseline (no state) without touching the
// uplink resolver configuration.
func (p *Plan) AcquireLinuxResolvedDNS(ctx context.Context, tunName, dnsIP string) (*Lease, error) {
	if tunName == "" || strings.ContainsAny(tunName, " \t\r\n") {
		return nil, fmt.Errorf("invalid Linux DNS interface %q", tunName)
	}
	address := net.ParseIP(dnsIP).To4()
	if address == nil {
		return nil, fmt.Errorf("invalid Linux IPv4 DNS server %q", dnsIP)
	}
	index, lookupErr := linuxLinkIndex(tunName)
	if lookupErr != nil {
		return nil, lookupErr
	}
	if index <= 0 || index > math.MaxInt32 {
		return nil, fmt.Errorf("invalid resolved interface index %d", index)
	}
	linkIndex := int32(index)
	revert := func(cleanupCtx context.Context) error {
		current, err := linuxLinkIndex(tunName)
		if err != nil {
			return fmt.Errorf("verify resolved link ownership: %w", err)
		}
		if current != index {
			return fmt.Errorf("resolved interface %s changed index from %d to %d", tunName, index, current)
		}
		return linuxResolvedCall(cleanupCtx, "RevertLink", linkIndex)
	}

	// Record ownership before the first call: even a failed D-Bus exchange can
	// have changed the OS state before its reply was lost.
	lease, err := p.Acquire("resolved-dns "+tunName, func() error { return nil }, revert)
	if err != nil {
		return nil, err
	}
	for _, call := range []struct {
		method string
		args   []any
	}{
		{"SetLinkDNS", []any{linkIndex, []resolvedAddress{{Family: unix.AF_INET, Address: []byte(address)}}}},
		{"SetLinkDomains", []any{linkIndex, []resolvedDomain{{Domain: ".", RoutingOnly: true}}}},
		{"SetLinkDefaultRoute", []any{linkIndex, true}},
	} {
		if err := linuxResolvedCall(ctx, call.method, call.args...); err != nil {
			if cleanupErr := lease.Close(sessionapi.CleanupContext(ctx)); cleanupErr != nil {
				return lease, &sessionapi.CleanupFailure{Err: errors.Join(err, cleanupErr)}
			}
			return nil, err
		}
	}
	return lease, nil
}

// AcquireLinuxIPv6Block uses add, not replace, so pre-existing block routes
// are preserved. Only routes created by this plan receive a cleanup lease.
func (p *Plan) AcquireLinuxIPv6Block() error {
	for _, subnet := range []string{ipv6LowerHalf, ipv6UpperHalf} {
		subnet := subnet
		_, network, err := net.ParseCIDR(subnet)
		if err != nil {
			return fmt.Errorf("parse IPv6 block route %q: %w", subnet, err)
		}
		route := netlink.Route{
			Dst:      network,
			Protocol: linuxOwnedRouteProtocol,
			Priority: 1,
			Family:   netlink.FAMILY_V6,
			Table:    unix.RT_TABLE_MAIN,
			Type:     unix.RTN_BLACKHOLE,
		}
		created := false
		_, err = p.Acquire("ipv6-block "+subnet, func() error {
			if routeErr := linuxRouteOperation("add", &route, linuxRouteAdd); routeErr != nil {
				if linuxAlreadyExists(routeErr) {
					return nil
				}
				return routeErr
			}
			created = true
			return nil
		}, func(_ context.Context) error {
			if !created {
				return nil
			}
			routeErr := linuxRouteOperation("delete", &route, linuxRouteDel)
			if linuxRouteAlreadyGone(routeErr) {
				return nil
			}
			return routeErr
		})
		if err != nil {
			return err
		}
	}
	return nil
}
