//go:build darwin && !(android || ios)

package routing

import (
	"fmt"
	"strings"
)

// macosRunCommand is a narrow command seam for the session routing plan. It
// deliberately accepts only the routing package's fixed command strings.
var macosRunCommand = ExecuteCommand

type macOSDefaultRoute struct {
	gateway string
	iface   string
	flags   string
}

// AcquireMacOSProxyRoute installs the exact server bypass only if this
// generation created it. A route that predates the session is never removed.
func (p *Plan) AcquireMacOSProxyRoute(proxyIP, gatewayIP string) (*Lease, error) {
	if isLoopbackIP(proxyIP) {
		return nil, nil
	}

	created := false
	command := fmt.Sprintf("route -n add -host %s %s", proxyIP, gatewayIP)
	return p.Acquire("proxy-route", func() error {
		out, err := macosRunCommand(command)
		if err != nil {
			if macOSRouteExists(out, err) {
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
		_, err := macosRunCommand(fmt.Sprintf("route -n delete -host %s %s", proxyIP, gatewayIP))
		return err
	})
}

// AcquireMacOSTunnelDefault snapshots the active default before changing it
// to the session TUN. Cleanup first proves that the current default is still
// our TUN, then changes it back to the exact captured gateway/interface.
func (p *Plan) AcquireMacOSTunnelDefault(tunName string) (*Lease, error) {
	var baseline macOSDefaultRoute
	return p.Acquire("tun-default "+tunName, func() error {
		out, err := macosRunCommand("route -n get default")
		if err != nil {
			return fmt.Errorf("capture default route: %w", err)
		}
		baseline, err = macOSParseDefaultRoute(out)
		if err != nil {
			return err
		}
		_, err = macosRunCommand(fmt.Sprintf("route -n change default -interface %s", tunName))
		return err
	}, func() error {
		out, err := macosRunCommand("route -n get default")
		if err != nil {
			return fmt.Errorf("verify owned default route: %w", err)
		}
		current, err := macOSParseDefaultRoute(out)
		if err != nil {
			return fmt.Errorf("parse current default route: %w", err)
		}
		if current.iface != tunName {
			return fmt.Errorf("session default is no longer owned by TUN %q", tunName)
		}
		_, err = macosRunCommand(macOSRestoreDefaultCommand(baseline))
		return err
	})
}

// AcquireMacOSIPv6Block adds each sink route without pre-deleting a possibly
// pre-existing route. Only routes created by this plan are released.
func (p *Plan) AcquireMacOSIPv6Block(tunName string) error {
	for _, subnet := range ipv6DefaultSubnets {
		subnet := subnet
		created := false
		if _, err := p.Acquire("ipv6-block "+subnet, func() error {
			out, err := macosRunCommand(fmt.Sprintf("route -n add -inet6 -net %s -interface %s", subnet, tunName))
			if err != nil {
				if macOSRouteExists(out, err) {
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
			_, err := macosRunCommand(fmt.Sprintf("route -n delete -inet6 -net %s -interface %s", subnet, tunName))
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

// AcquireMacOSScopedDefault creates the direct-traffic bypass only if this
// session created it. It is released before the tunnel default (LIFO).
func (p *Plan) AcquireMacOSScopedDefault(iface, gatewayIP string) (*Lease, error) {
	if iface == "" {
		return nil, nil
	}
	created := false
	command := fmt.Sprintf("route -n add default %s -ifscope %s", gatewayIP, iface)
	return p.Acquire("scoped-default "+iface, func() error {
		out, err := macosRunCommand(command)
		if err != nil {
			if macOSRouteExists(out, err) {
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
		_, err := macosRunCommand(fmt.Sprintf("route -n delete default %s -ifscope %s", gatewayIP, iface))
		return err
	})
}

func macOSRouteExists(out string, err error) bool {
	return strings.Contains(out, "File exists") || (err != nil && strings.Contains(err.Error(), "File exists"))
}

func macOSParseDefaultRoute(output string) (macOSDefaultRoute, error) {
	var route macOSDefaultRoute
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		switch key {
		case "gateway":
			route.gateway = value
		case "interface":
			route.iface = value
		case "flags":
			route.flags = value
		}
	}
	if route.iface == "" {
		return macOSDefaultRoute{}, fmt.Errorf("default route has no interface")
	}
	if route.gateway == "" {
		return macOSDefaultRoute{}, fmt.Errorf("default route has no gateway")
	}
	return route, nil
}

func macOSRestoreDefaultCommand(route macOSDefaultRoute) string {
	if strings.HasPrefix(route.gateway, "link#") {
		return fmt.Sprintf("route -n change default -interface %s", route.iface)
	}
	// -ifscope only matches a default route which was scoped before this
	// generation. Applying it to the usual unscoped DHCP default can remove the
	// active default without installing the replacement. For the unscoped case,
	// route resolves the captured gateway via its original interface.
	if strings.Contains(route.flags, "IFSCOPE") {
		return fmt.Sprintf("route -n change default %s -ifscope %s", route.gateway, route.iface)
	}
	return fmt.Sprintf("route -n change default %s", route.gateway)
}
