//go:build windows && !(android || ios)

package protected_dialer

import (
	"core/routing"
	"fmt"
	"net/netip"
)

func selectCompatibleUnderlays(network string, addresses []string, preferred uint64, selectRoute func(netip.Addr, uint64) (routing.UnderlayRoute, error)) (routing.UnderlayRoute, error) {
	ipv4 := network == networkTCP4 || network == networkUDP4
	if !ipv4 && network != networkTCP6 && network != networkUDP6 {
		return routing.UnderlayRoute{}, fmt.Errorf("unsupported protected network %q", network)
	}
	var selected routing.UnderlayRoute
	for _, address := range addresses {
		ip, err := routing.UnderlayDestination(address)
		if err != nil {
			return selected, err
		}
		if ip.Is4() != ipv4 {
			return selected, fmt.Errorf("destination %s does not match %s", address, network)
		}
		selectionPreference := preferred
		if selected.LUID != 0 {
			selectionPreference = selected.LUID
		}
		candidate, err := selectRoute(ip, selectionPreference)
		if err != nil {
			return selected, err
		}
		if selected.LUID != 0 && (selected.LUID != candidate.LUID || selected.Index != candidate.Index || selected.GUID != candidate.GUID) {
			return selected, fmt.Errorf("native socket destinations select incompatible underlays: %d and %d", selected.LUID, candidate.LUID)
		}
		selected = candidate
	}
	if selected.LUID == 0 {
		return selected, fmt.Errorf("no socket destinations")
	}
	return selected, nil
}
