//go:build darwin && !ios

package platform_engine

import "net"

// A /128 was assigned on macOS but ordinary sockets could not select it as
// their IPv6 source. A /120 supports both local and routed destinations.
const MacOSIPv6PrefixLength = 120

// MacOSPointToPoint describes the addresses used in the packet tunnel network settings.
type MacOSPointToPoint struct {
	Local string `json:"local"`
	Peer  string `json:"peer"`
	Mask  string `json:"mask"`
}

func MacOSTunnelAddresses() (ipv4, ipv6 MacOSPointToPoint) {
	return MacOSPointToPoint{Local: "198.18.0.1", Peer: "198.18.0.2", Mask: "255.255.0.0"},
		MacOSPointToPoint{Local: "fd00:dbb::2", Peer: "fd00:dbb::1", Mask: net.IP(net.CIDRMask(MacOSIPv6PrefixLength, 128)).String()}
}
