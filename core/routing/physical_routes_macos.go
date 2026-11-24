//go:build darwin && !ios && cgo

package routing

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"sync"

	"core/log"

	"golang.org/x/sys/unix"
)

// MacOSPhysicalRoutes keeps native, interface-scoped resolver traffic on the
// underlying network while the unscoped application defaults point at utun.
// Its nested plan bounds ownership to the current physical route set. A link
// change closes that set before creating another; failed cleanup blocks it.
type MacOSPhysicalRoutes struct {
	mu        sync.Mutex
	sessionID string
	current   *Plan
	routes    []macOSRoute
	identity  string
	complete  bool
	closed    bool
}

func (p *Plan) AcquireMacOSPhysicalRoutes() (*MacOSPhysicalRoutes, error) {
	physical := &MacOSPhysicalRoutes{sessionID: p.sessionID + "/physical"}
	_, err := p.Acquire("physical interface-scoped defaults", func() error { return nil }, physical.Close)
	if err != nil {
		return nil, err
	}
	return physical, nil
}

// Update captures actual physical gateways for both families. It never repairs
// a gateway from an obsolete physical context or mutates an unscoped default.
func (p *MacOSPhysicalRoutes) Update(ctx context.Context, physical MacOSPhysicalNetwork) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errors.New("physical macOS routes are closed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// The identity covers gateways and DNS of the physical services, so an
	// unchanged one needs no routing table dump.
	if physical.Identity != "" && p.complete && p.identity == physical.Identity {
		return nil
	}
	routes, err := macOSPhysicalScopedDefaults(physical)
	if err != nil {
		return err
	}
	same := p.complete && len(routes) == len(p.routes)
	for i := range routes {
		if !same || !routes[i].same(p.routes[i]) {
			same = false
			break
		}
	}
	if same {
		for _, route := range routes {
			current, err := findMacOSRoute(route)
			if err != nil {
				return err
			}
			if current == nil {
				same = false
			}
		}
		if same {
			p.identity = physical.Identity
			return nil
		}
	}
	// A partly closed plan no longer matches the remembered identity.
	p.complete = false
	if p.current != nil {
		if err := p.current.Close(ctx); err != nil {
			return err
		}
	}
	// Record the replacement before applying anything so partial acquisition is
	// retained for cleanup, including a successful native write followed by error.
	p.current = NewPlan(p.sessionID)
	p.routes = routes
	for _, route := range routes {
		if _, err := p.current.acquireMacOSRoute(ctx, route); err != nil {
			return err
		}
	}
	p.complete = true
	p.identity = physical.Identity
	for _, route := range routes {
		log.Info(Category, "macOS physical scoped default retained", map[string]any{
			"network_identity": physical.Identity, "prefix": route.prefix.String(),
			"gateway": route.gateway.String(), "interface_index": route.index, "flags": route.flags,
		})
	}
	return nil
}

// macOSLinkCandidate is one network interface as MacOSPFLinks sees it.
type macOSLinkCandidate struct {
	name      string
	index     int
	flags     net.Flags
	addresses []netip.Addr
}

// MacOSPFLinks inventories every physical interface that can receive inbound
// connections, for each address family it has a usable address of, with the
// gateway of its default route (the primary or the interface-scoped one), so
// replies to inbound connections from excluded networks can leave through
// whichever interface the connection came in on, Wi-Fi and Ethernet alike.
func MacOSPFLinks() ([]PFLink, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list interfaces: %w", err)
	}
	candidates := make([]macOSLinkCandidate, 0, len(interfaces))
	for _, iface := range interfaces {
		addresses, err := iface.Addrs()
		if err != nil {
			return nil, fmt.Errorf("addresses of %s: %w", iface.Name, err)
		}
		candidate := macOSLinkCandidate{name: iface.Name, index: iface.Index, flags: iface.Flags}
		for _, address := range addresses {
			if prefix, err := netip.ParsePrefix(address.String()); err == nil {
				candidate.addresses = append(candidate.addresses, prefix.Addr())
			}
		}
		candidates = append(candidates, candidate)
	}
	routes, err := macOSReadRoutes()
	if err != nil {
		return nil, err
	}
	return macOSPFLinks(candidates, routes), nil
}

// macOSPFLinks keeps interfaces that are up and neither loopback nor
// point-to-point (the tunnel, IPsec), per family with an address that is not
// link-local, and takes the gateway from that interface's default route.
func macOSPFLinks(candidates []macOSLinkCandidate, routes []macOSRoute) []PFLink {
	var links []PFLink
	for _, candidate := range candidates {
		if candidate.flags&net.FlagUp == 0 || candidate.flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 {
			continue
		}
		for _, ipv6 := range []bool{false, true} {
			if !slices.ContainsFunc(candidate.addresses, func(address netip.Addr) bool {
				address = address.Unmap()
				return address.Is6() == ipv6 && !address.IsLinkLocalUnicast() && !address.IsLoopback()
			}) {
				continue
			}
			link := PFLink{Interface: candidate.name, IPv6: ipv6}
			for _, route := range routes {
				if route.index != candidate.index || !route.prefix.IsValid() || route.prefix.Bits() != 0 ||
					route.prefix.Addr().Is6() != ipv6 || !route.gateway.IsValid() || route.flags&unix.RTF_UP == 0 ||
					route.flags&(unix.RTF_REJECT|unix.RTF_BLACKHOLE) != 0 {
					continue
				}
				// Prefer the primary (unscoped) default over a scoped copy.
				if !link.Gateway.IsValid() || route.flags&unix.RTF_IFSCOPE == 0 {
					link.Gateway = route.gateway
				}
			}
			links = append(links, link)
		}
	}
	return links
}

func (p *MacOSPhysicalRoutes) Close(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	if p.current == nil {
		return nil
	}
	if err := p.current.Close(ctx); err != nil {
		return err
	}
	p.current = nil
	p.routes = nil
	p.complete = false
	return nil
}

func macOSPhysicalScopedDefaults(physical MacOSPhysicalNetwork) ([]macOSRoute, error) {
	rows, err := macOSReadRoutes()
	if err != nil {
		return nil, err
	}
	var defaults []macOSRoute
	for _, family := range []struct {
		index int
		ipv4  bool
	}{{physical.IPv4, true}, {physical.IPv6, false}} {
		if family.index == 0 {
			continue
		}
		var selected *macOSRoute
		for _, row := range rows {
			if !row.prefix.IsValid() || row.prefix.Bits() != 0 || row.prefix.Addr().Is4() != family.ipv4 ||
				row.index != family.index || row.flags&unix.RTF_UP == 0 ||
				row.flags&(unix.RTF_IFSCOPE|unix.RTF_REJECT|unix.RTF_BLACKHOLE|unix.RTF_WASCLONED|unix.RTF_LLINFO) != 0 {
				continue
			}
			if selected != nil && !selected.same(row) {
				return nil, fmt.Errorf("ambiguous physical macOS default on interface %d: %+v and %+v", family.index, *selected, row)
			}
			copy := row
			selected = &copy
		}
		if selected == nil {
			continue
		}
		want := macOSRoute{prefix: selected.prefix, gateway: selected.gateway, index: selected.index,
			flags: unix.RTF_UP | unix.RTF_STATIC | unix.RTF_IFSCOPE}
		if want.gateway.IsValid() {
			want.flags |= unix.RTF_GATEWAY
		}
		defaults = append(defaults, want)
	}
	if len(defaults) == 0 {
		return nil, errors.New("no physical macOS IPv4 or IPv6 default route")
	}
	return defaults, nil
}
