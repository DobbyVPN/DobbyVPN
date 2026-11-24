//go:build darwin && !ios && cgo

package routing

import (
	"context"
	"errors"
	"fmt"
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
	routes    []*macOSRouteOwner
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
	return p.update(ctx, physical, false)
}

// Refresh checks the kernel routes even when SystemConfiguration is unchanged.
func (p *MacOSPhysicalRoutes) Refresh(ctx context.Context, physical MacOSPhysicalNetwork) error {
	return p.update(ctx, physical, true)
}

func (p *MacOSPhysicalRoutes) update(ctx context.Context, physical MacOSPhysicalNetwork, refresh bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errors.New("physical macOS routes are closed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Resolver lookups may reuse a completed observation for the same service identity.
	if !refresh && physical.Identity != "" && p.complete && p.identity == physical.Identity {
		return nil
	}
	// A failed observation must be retried even by an ordinary resolver lookup.
	p.identity = ""
	rows, err := macOSReadRoutes()
	if err != nil {
		return err
	}
	routes, err := macOSPhysicalScopedDefaults(physical, rows)
	if err != nil {
		return err
	}
	same := p.complete && len(routes) == len(p.routes)
	for i := range routes {
		if !same || !routes[i].same(p.routes[i].macOSRoute) {
			same = false
			break
		}
	}
	if same {
		var missing []*macOSRouteOwner
		var conflict error
		for _, owner := range p.routes {
			current, err := matchMacOSRoute(rows, owner.macOSRoute)
			if err != nil {
				conflict = errors.Join(conflict, err)
				continue
			}
			if current == nil {
				owner.owned = false
				missing = append(missing, owner)
			}
		}
		if conflict != nil {
			return conflict
		}
		for _, owner := range missing {
			changed, err := owner.add(ctx)
			if err != nil {
				return err
			}
			if changed {
				log.Info(Category, "macOS physical scoped default restored", map[string]any{
					"network_identity": physical.Identity, "prefix": owner.prefix.String(), "interface_index": owner.index,
				})
			}
		}
		p.identity = physical.Identity
		return nil
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
	p.routes = nil
	for _, route := range routes {
		owner, err := p.current.acquireMacOSRoute(ctx, route)
		if err != nil {
			return err
		}
		p.routes = append(p.routes, owner)
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

func macOSPhysicalScopedDefaults(physical MacOSPhysicalNetwork, rows []macOSRoute) ([]macOSRoute, error) {
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
