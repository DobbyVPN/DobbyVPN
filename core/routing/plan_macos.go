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

// ErrForeignDefaultRoutes reports a route using a default-route half this
// session needs. The route may come from a VPN or another configuration.
var ErrForeignDefaultRoutes = errors.New("network routes needed by DobbyVPN are already in use; disconnect any other VPN or adjust the conflicting network settings, then try again")

func isDefaultSplitPrefix(prefix netip.Prefix) bool {
	for _, subnet := range append(append([]string{}, ipv4DefaultSubnets...), ipv6DefaultSubnets...) {
		if prefix == netip.MustParsePrefix(subnet) {
			return true
		}
	}
	return false
}

func (p *Plan) AcquireMacOSIPv4Default(ctx context.Context, tunName string) error {
	return p.acquireMacOSInterfaceRoutes(ctx, ipv4DefaultSubnets, tunName)
}

func (p *Plan) AcquireMacOSIPv6Default(ctx context.Context, tunName string) error {
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
	return matchMacOSRoute(rows, want)
}

func matchMacOSRoute(rows []macOSRoute, want macOSRoute) (*macOSRoute, error) {
	var exact *macOSRoute
	for _, row := range rows {
		if row.prefix != want.prefix || row.flags&unix.RTF_IFSCOPE != want.flags&unix.RTF_IFSCOPE ||
			want.flags&unix.RTF_IFSCOPE != 0 && row.index != want.index {
			continue
		}
		// Ignore OS-generated neighbour clones; they do not own static policy.
		if row.flags&(unix.RTF_WASCLONED|unix.RTF_LLINFO) != 0 {
			continue
		}
		if !want.same(row) {
			if isDefaultSplitPrefix(want.prefix) {
				return nil, fmt.Errorf("%w (route %s gateway=%s interface=%d flags=%#x)", ErrForeignDefaultRoutes, row.prefix, row.gateway, row.index, row.flags)
			}
			return nil, fmt.Errorf("conflicting route %s gateway=%s interface=%d flags=%#x", row.prefix, row.gateway, row.index, row.flags)
		}
		copy := row
		exact = &copy
	}
	return exact, nil
}

// A missing row is restored with an exclusive add. If another writer wins
// the race, the exact row can be used without taking ownership of it.
type macOSRouteOwner struct {
	macOSRoute
	owned bool
}

func (r *macOSRouteOwner) add(ctx context.Context) (bool, error) {
	var err error
	r.owned, err = macOSChangeRoute(ctx, unix.RTM_ADD, r.macOSRoute)
	if errors.Is(err, unix.EEXIST) {
		current, err := findMacOSRoute(r.macOSRoute)
		if err == nil && current == nil {
			err = fmt.Errorf("route exists but exact identity was not found: %s", r.prefix)
		}
		return false, err
	}
	if err != nil {
		return r.owned, err
	}
	current, err := findMacOSRoute(r.macOSRoute)
	if err == nil && current == nil {
		err = fmt.Errorf("created route %s is absent", r.prefix)
	}
	return true, err
}

func (p *Plan) acquireMacOSRoute(ctx context.Context, want macOSRoute) (*macOSRouteOwner, error) {
	owner := &macOSRouteOwner{macOSRoute: want}
	ensure := func() (bool, error) {
		current, err := findMacOSRoute(want)
		if err != nil || current != nil {
			return false, err
		}
		return owner.add(ctx)
	}
	lease, err := p.Acquire("route "+want.prefix.String(), func() error { return nil }, func(cleanupCtx context.Context) error {
		if !owner.owned {
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
	return owner, err
}
