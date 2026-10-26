//go:build darwin && !(android || ios)

package routing

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"golang.org/x/sys/unix"
)

var ipv4DefaultSubnets = []string{"0.0.0.0/1", "128.0.0.0/1"}

var ipv4DefaultRoute = netip.MustParsePrefix("0.0.0.0/0")

func (p *Plan) AcquireMacOSProxyRoute(ctx context.Context, proxyIP, gatewayIP, interfaceName string) (*Lease, error) {
	if isLoopbackIP(proxyIP) {
		return nil, nil
	}
	prefix, err := netip.ParsePrefix(proxyIP + "/32")
	if err != nil {
		return nil, err
	}
	gateway, err := netip.ParseAddr(gatewayIP)
	if err != nil {
		return nil, err
	}
	iface, err := macOSInterface(interfaceName)
	if err != nil {
		return nil, err
	}
	return p.acquireMacOSRoute(ctx, macOSRoute{prefix, gateway, iface.Index, unix.RTF_UP | unix.RTF_STATIC | unix.RTF_GATEWAY | unix.RTF_HOST})
}

// AcquireMacOSPhysicalDefault owns a default route scoped to the physical
// interface for this session. Protected sockets are bound to that interface
// with IP_BOUND_IF, so the kernel resolves them inside its scope, where this
// session's own 0.0.0.0/1 and 128.0.0.0/1 shadow the unscoped default without
// belonging to the scope. Darwin creates such a scoped route only for routes
// it manages itself, so without this lease every direct connection outside the
// tunnel fails immediately with ENETUNREACH.
func (p *Plan) AcquireMacOSPhysicalDefault(ctx context.Context, gatewayIP, interfaceName string) (*Lease, error) {
	gateway, err := netip.ParseAddr(gatewayIP)
	if err != nil {
		return nil, err
	}
	iface, err := macOSInterface(interfaceName)
	if err != nil {
		return nil, err
	}
	return p.acquireMacOSRoute(ctx, macOSRoute{ipv4DefaultRoute, gateway, iface.Index, unix.RTF_UP | unix.RTF_STATIC | unix.RTF_GATEWAY | unix.RTF_IFSCOPE})
}

func (p *Plan) AcquireMacOSIPv4Default(ctx context.Context, tunName string) error {
	return p.acquireMacOSInterfaceRoutes(ctx, ipv4DefaultSubnets, tunName)
}

func (p *Plan) AcquireMacOSIPv6Block(ctx context.Context, tunName string) error {
	return p.acquireMacOSInterfaceRoutes(ctx, ipv6DefaultSubnets, tunName)
}

func (p *Plan) acquireMacOSInterfaceRoutes(ctx context.Context, subnets []string, name string) error {
	iface, err := macOSInterface(name)
	if err != nil {
		return err
	}
	for _, subnet := range subnets {
		if _, err := p.acquireMacOSRoute(ctx, macOSRoute{netip.MustParsePrefix(subnet), netip.Addr{}, iface.Index, unix.RTF_UP | unix.RTF_STATIC}); err != nil {
			return err
		}
	}
	return nil
}

// Exact foreign routes may be reused, but never adopted. A conflicting route
// at the same destination is an error; neither setup nor repair deletes it.
func findMacOSRoute(want macOSRoute) (*macOSRoute, error) {
	rows, err := macOSReadRoutes()
	if err != nil {
		return nil, err
	}
	var exact *macOSRoute
	for _, row := range rows {
		if row.prefix != want.prefix {
			continue
		}
		// Ignore OS-generated neighbour clones; they do not own static policy.
		if row.flags&(unix.RTF_WASCLONED|unix.RTF_LLINFO) != 0 {
			continue
		}
		// A route in a different lookup namespace is not competing policy.
		if !want.sameScope(row) {
			continue
		}
		if !want.same(row) {
			return nil, fmt.Errorf("conflicting route %s gateway=%s interface=%d flags=%#x", row.prefix, row.gateway, row.index, row.flags)
		}
		copy := row
		exact = &copy
	}
	return exact, nil
}

func (p *Plan) acquireMacOSRoute(ctx context.Context, want macOSRoute) (*Lease, error) {
	owned := false
	ensure := func() (bool, error) {
		current, err := findMacOSRoute(want)
		if err != nil || current != nil {
			return false, err
		}
		owned, err = macOSChangeRoute(ctx, unix.RTM_ADD, want)
		if errors.Is(err, unix.EEXIST) {
			current, err = findMacOSRoute(want)
			if err == nil && current == nil {
				err = fmt.Errorf("route exists but exact identity was not found: %s", want.prefix)
			}
			return false, err
		}
		// A successful atomic write owns the mutation even if socket close or
		// verification fails. A rejected write never adopts a foreign route.
		if err != nil {
			return false, err
		}
		current, err = findMacOSRoute(want)
		if err != nil {
			return true, err
		}
		if current == nil {
			return true, fmt.Errorf("created route %s is absent", want.prefix)
		}
		return true, nil
	}
	lease, err := p.Acquire("route "+want.prefix.String(), func() error { return nil }, func(cleanupCtx context.Context) error {
		if !owned {
			return nil
		}
		current, err := findMacOSRoute(want)
		if err != nil || current == nil {
			return err
		}
		_, deleteErr := macOSChangeRoute(cleanupCtx, unix.RTM_DELETE, *current)
		remaining, err := findMacOSRoute(want)
		if err != nil {
			return errors.Join(deleteErr, err)
		}
		if remaining != nil {
			return errors.Join(deleteErr, fmt.Errorf("route %s remains after deletion", want.prefix))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	lease.mu.Lock()
	lease.repair = ensure
	_, err = ensure()
	lease.mu.Unlock()
	return lease, err
}
