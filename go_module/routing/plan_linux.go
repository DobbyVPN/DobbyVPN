//go:build linux && !(android || ios)

package routing

import (
	"fmt"
	"strings"
)

func linuxRouteAlreadyGone(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such process") ||
		strings.Contains(message, "no such file or directory") || strings.Contains(message, "cannot find")
}

// AcquireLinuxProxyRoute installs a host bypass only when this session added
// it. Existing routes are left untouched and therefore are never removed by
// the lease.
func (p *Plan) AcquireLinuxProxyRoute(proxyIP, gatewayIP, iface string) (*Lease, error) {
	if isLoopbackIP(proxyIP) {
		return nil, nil
	}

	command := fmt.Sprintf(
		"ip route add %s/32 via %s dev %s proto %d metric %d",
		proxyIP, gatewayIP, iface, linuxOwnedRouteProtocol, linuxOwnedProxyMetric,
	)
	var created bool
	return p.Acquire("proxy-route "+proxyIP, func() error {
		_, err := linuxRunCommand(command)
		if err != nil {
			if strings.Contains(err.Error(), "File exists") {
				return nil
			}
			return err
		}
		created = true
		return nil
	}, func() error {
		if !created {
			return nil
		}
		_, err := linuxRunCommand(fmt.Sprintf(
			"ip route del %s/32 via %s dev %s proto %d metric %d",
			proxyIP, gatewayIP, iface, linuxOwnedRouteProtocol, linuxOwnedProxyMetric,
		))
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
	routeCommand := fmt.Sprintf(
		"ip route add table %d %s via %s dev %s proto %d",
		tableID, linuxDefaultRoute, gatewayIP, iface, linuxOwnedRouteProtocol,
	)
	routeDelete := fmt.Sprintf(
		"ip route del table %d %s via %s dev %s proto %d",
		tableID, linuxDefaultRoute, gatewayIP, iface, linuxOwnedRouteProtocol,
	)
	routeLease, err := p.Acquire(fmt.Sprintf("mark-route table=%d", tableID), func() error {
		_, err := linuxRunCommand(routeCommand)
		return err
	}, func() error {
		_, err := linuxRunCommand(routeDelete)
		if linuxRouteAlreadyGone(err) {
			return nil
		}
		return err
	})
	if err != nil {
		return err
	}

	ruleCommand := fmt.Sprintf("ip rule add fwmark %d lookup %d priority %d", tableID, tableID, priority)
	ruleDelete := fmt.Sprintf("ip rule del fwmark %d lookup %d priority %d", tableID, tableID, priority)
	if _, err := p.Acquire(fmt.Sprintf("mark-rule table=%d priority=%d", tableID, priority), func() error {
		_, err := linuxRunCommand(ruleCommand)
		return err
	}, func() error {
		_, err := linuxRunCommand(ruleDelete)
		return err
	}); err != nil {
		_ = routeLease.Close()
		return err
	}
	return nil
}

// AcquireLinuxTunnelDefault replaces the active default route with the TUN
// route and records the exact previous route. Release deletes only the TUN
// default and restores that recorded baseline; it never guesses a gateway.
func (p *Plan) AcquireLinuxTunnelDefault(tunName string) (*Lease, error) {
	var baseline string
	return p.Acquire("tun-default "+tunName, func() error {
		output, err := linuxRunCommand("ip -o -4 route show " + linuxDefaultRoute)
		if err != nil {
			return fmt.Errorf("capture default route: %w", err)
		}
		baseline, err = linuxDefaultRouteRestoreCommand(output)
		if err != nil {
			return err
		}
		_, err = linuxRunCommand(fmt.Sprintf(
			"ip route replace %s dev %s proto %d", linuxDefaultRoute, tunName, linuxOwnedRouteProtocol,
		))
		return err
	}, func() error {
		// Specify the TUN device so a changed default owned by another actor is
		// never removed. Do not restore the snapshot if that deletion fails.
		if _, err := linuxRunCommand(fmt.Sprintf(
			"ip route del %s dev %s proto %d", linuxDefaultRoute, tunName, linuxOwnedRouteProtocol,
		)); err != nil {
			return err
		}
		_, err := linuxRunCommand(baseline)
		return err
	})
}

func linuxDefaultRouteRestoreCommand(output string) (string, error) {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if fields[0] == linuxDefaultRoute {
			return "ip route replace " + strings.Join(fields, " "), nil
		}
	}
	return "", fmt.Errorf("no IPv4 default route to preserve")
}

// AcquireLinuxIPv6Block uses add, not replace, so pre-existing block routes
// are preserved. Only routes created by this plan receive a cleanup lease.
func (p *Plan) AcquireLinuxIPv6Block() error {
	for _, subnet := range []string{ipv6LowerHalf, ipv6UpperHalf} {
		subnet := subnet
		created := false
		_, err := p.Acquire("ipv6-block "+subnet, func() error {
			_, err := linuxRunCommand(fmt.Sprintf(
				"ip -6 route add blackhole %s proto %d metric 1", subnet, linuxOwnedRouteProtocol,
			))
			if err != nil {
				if strings.Contains(err.Error(), "File exists") {
					return nil
				}
				return err
			}
			created = true
			return nil
		}, func() error {
			if !created {
				return nil
			}
			_, err := linuxRunCommand(fmt.Sprintf(
				"ip -6 route del blackhole %s proto %d metric 1", subnet, linuxOwnedRouteProtocol,
			))
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}
