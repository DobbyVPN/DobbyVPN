//go:build (windows || darwin) && !ios && !android

package protected_dialer

import (
	"fmt"
	"sync/atomic"
)

type physicalInterfacePair struct {
	IPv4 int
	IPv6 int
}

var physicalInterfaces atomic.Pointer[physicalInterfacePair]

// SetPhysicalInterfaces atomically updates the physical interface indices used
// to protect IPv4 and IPv6 sockets.
func SetPhysicalInterfaces(ipv4, ipv6 int) {
	physicalInterfaces.Store(&physicalInterfacePair{IPv4: ipv4, IPv6: ipv6})
}

// ResetDefaultRoute clears the generation-owned interface binding. It must be
// called when a session exits, including failed starts, so later protected
// sockets cannot inherit a stale physical interface.
func ResetDefaultRoute() {
	physicalInterfaces.Store(nil)
}

func physicalInterfaceIndex(network string) (int, error) {
	pair := physicalInterfaces.Load()
	var index int
	switch network {
	case networkTCP4, networkUDP4:
		if pair != nil {
			index = pair.IPv4
		}
	case networkTCP6, networkUDP6:
		if pair != nil {
			index = pair.IPv6
		}
	default:
		return 0, fmt.Errorf("unsupported protected network %q", network)
	}
	if index <= 0 {
		return 0, ErrSocketProtectionUnavailable
	}
	return index, nil
}
