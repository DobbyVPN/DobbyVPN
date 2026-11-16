package routing

import (
	"fmt"
	"net"
	"net/netip"
)

// UnderlayRoute is a validated route and its live interface identity.
type UnderlayRoute struct {
	Prefix          netip.Prefix
	NextHop         netip.Addr
	LUID            uint64
	Index           uint32
	GUID            string
	RouteMetric     uint32
	InterfaceMetric uint32
}

// SelectUnderlay chooses the OS-selected eligible route where possible. When
// the OS chose the owned tunnel, use longest prefix and combined metric, keeping
// an existing equally good interface to avoid unnecessary route churn.
func SelectUnderlay(destination netip.Addr, candidates []UnderlayRoute, osLUID, preferredLUID uint64) (UnderlayRoute, error) {
	destination = destination.Unmap()
	eligible := make([]UnderlayRoute, 0, len(candidates))
	for _, c := range candidates {
		if c.LUID != 0 && c.Index != 0 && c.Prefix.IsValid() && c.Prefix.Contains(destination) {
			eligible = append(eligible, c)
		}
	}
	if len(eligible) == 0 {
		return UnderlayRoute{}, fmt.Errorf("no eligible underlay route for %s", destination)
	}
	better := func(a, b UnderlayRoute) bool {
		if a.Prefix.Bits() != b.Prefix.Bits() {
			return a.Prefix.Bits() > b.Prefix.Bits()
		}
		am, bm := uint64(a.RouteMetric)+uint64(a.InterfaceMetric), uint64(b.RouteMetric)+uint64(b.InterfaceMetric)
		if am != bm {
			return am < bm
		}
		if (a.LUID == preferredLUID) != (b.LUID == preferredLUID) {
			return a.LUID == preferredLUID
		}
		if a.LUID != b.LUID {
			return a.LUID < b.LUID
		}
		return a.GUID < b.GUID
	}
	// Multiple routes may share the OS-selected interface; retain the best row
	// on that interface for accurate diagnostics.
	var best UnderlayRoute
	found := false
	if osLUID != 0 {
		for _, c := range eligible {
			if c.LUID == osLUID && (!found || better(c, best)) {
				best = c
				found = true
			}
		}
	}
	if found {
		return best, nil
	}
	for _, c := range eligible {
		if !found || better(c, best) {
			best = c
			found = true
		}
	}
	return best, nil
}

// EligibleUnderlay rejects stale or owned interfaces without relying on names.
func EligibleUnderlay(prefix netip.Prefix, destination netip.Addr, luid uint64, index uint32, mappedLUID, ownedLUID uint64, up, loopback, live bool) bool {
	return prefix.IsValid() && prefix.Contains(destination.WithZone("")) && luid != 0 && index != 0 && luid == mappedLUID && (ownedLUID == 0 || luid != ownedLUID) && up && !loopback && live
}

// UnderlayDestination parses the concrete peer supplied to a socket control callback.
func UnderlayDestination(address string) (netip.Addr, error) {
	host := address
	if h, _, err := net.SplitHostPort(address); err == nil {
		host = h
	}
	destination, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("invalid numeric destination %q: %w", address, err)
	}
	return destination.Unmap(), nil
}

// PreferDefaultRoute stabilizes equal-metric default-route inventory without
// depending on route table order or adapter names.
func PreferDefaultRoute(metric, luid, bestMetric, bestLUID, preferred uint64) bool {
	if bestLUID == 0 || metric < bestMetric {
		return true
	}
	if metric > bestMetric {
		return false
	}
	if luid == preferred && bestLUID != preferred {
		return true
	}
	if bestLUID == preferred && luid != preferred {
		return false
	}
	return luid < bestLUID
}
