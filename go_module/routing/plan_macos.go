//go:build darwin && !(android || ios)

package routing

import (
	"fmt"
	"strings"
)

// macosRunCommand is a narrow command seam for the session routing plan. It
// deliberately accepts only the routing package's fixed command strings.
var macosRunCommand = ExecuteCommand

var ipv4DefaultSubnets = []string{"0.0.0.0/1", "128.0.0.0/1"}

// AcquireMacOSProxyRoute installs the exact server bypass. If the same route
// survived a killed predecessor, this generation adopts and later removes it.
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
				created = true
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

// AcquireMacOSIPv4Default routes both halves of IPv4 through the session TUN
// without replacing the physical default. Routes bound to a killed utun then
// disappear while the machine's ordinary default remains usable for restart.
func (p *Plan) AcquireMacOSIPv4Default(tunName string) error {
	return p.acquireMacOSInterfaceRoutes("ipv4-default", "", ipv4DefaultSubnets, tunName)
}

// AcquireMacOSIPv6Block adds each sink route without pre-deleting a possibly
// pre-existing route. An exact route left by a killed predecessor is adopted.
func (p *Plan) AcquireMacOSIPv6Block(tunName string) error {
	return p.acquireMacOSInterfaceRoutes("ipv6-block", "-inet6 ", ipv6DefaultSubnets, tunName)
}

func (p *Plan) acquireMacOSInterfaceRoutes(label, family string, subnets []string, tunName string) error {
	for _, subnet := range subnets {
		subnet := subnet
		owned := false
		if _, err := p.Acquire(label+" "+subnet, func() error {
			out, err := macosRunCommand(fmt.Sprintf("route -n add %s-net %s -interface %s", family, subnet, tunName))
			if err != nil {
				if macOSRouteExists(out, err) {
					owned = true
					return nil
				}
				return err
			}
			owned = true
			return nil
		}, func() error {
			if !owned {
				return nil
			}
			_, err := macosRunCommand(fmt.Sprintf("route -n delete %s-net %s -interface %s", family, subnet, tunName))
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

// RepairMacOSSessionRoutes restores the routes which macOS removes when the
// physical interface goes down. A present exact server route is the inexpensive
// signal; a fallback route through either the TUN or physical default is not.
func RepairMacOSSessionRoutes(proxyIP, gatewayIP, tunName, iface string) (bool, error) {
	if isLoopbackIP(proxyIP) {
		return false, nil
	}
	out, err := macosRunCommand(fmt.Sprintf("route -n get %s", proxyIP))
	if macOSRouteMissing(out, err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect proxy route for repair: %w", err)
	}
	healthy, err := macOSRouteIsExactHost(out, proxyIP, iface)
	if err != nil {
		return false, fmt.Errorf("parse proxy route for repair: %w", err)
	}
	if healthy {
		return false, nil
	}

	// macOS may retain the exact host route but re-resolve its gateway through
	// the TUN after the physical interface returns. Remove the two TUN routes
	// first so the fresh host route resolves through the untouched physical
	// default, then restore the TUN routes.
	for _, subnet := range ipv4DefaultSubnets {
		out, err = macosRunCommand(fmt.Sprintf("route -n delete -net %s -interface %s", subnet, tunName))
		if err != nil && !macOSRouteMissing(out, err) {
			return false, fmt.Errorf("remove tunnel route %s for repair: %w", subnet, err)
		}
	}
	out, err = macosRunCommand(fmt.Sprintf("route -n delete -host %s %s", proxyIP, gatewayIP))
	if err != nil && !macOSRouteMissing(out, err) {
		return false, fmt.Errorf("remove proxy route for repair: %w", err)
	}
	out, err = macosRunCommand(fmt.Sprintf("route -n add -host %s %s", proxyIP, gatewayIP))
	if err != nil && !macOSRouteExists(out, err) {
		return false, fmt.Errorf("restore proxy route: %w", err)
	}
	for _, subnet := range ipv4DefaultSubnets {
		out, err = macosRunCommand(fmt.Sprintf("route -n add -net %s -interface %s", subnet, tunName))
		if err != nil && !macOSRouteExists(out, err) {
			return false, fmt.Errorf("restore tunnel route %s: %w", subnet, err)
		}
	}
	return true, nil
}

func macOSRouteExists(out string, err error) bool {
	return strings.Contains(out, "File exists") || (err != nil && strings.Contains(err.Error(), "File exists"))
}

func macOSRouteMissing(out string, err error) bool {
	return strings.Contains(out, "not in table") || (err != nil && strings.Contains(err.Error(), "not in table"))
}

func macOSRouteIsExactHost(output, address, iface string) (bool, error) {
	var destination string
	var currentIface string
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		switch key {
		case "destination":
			destination = value
		case "interface":
			currentIface = value
		}
	}
	if destination == "" {
		return false, fmt.Errorf("route has no destination")
	}
	if currentIface == "" {
		return false, fmt.Errorf("route has no interface")
	}
	return destination == address && currentIface == iface, nil
}
