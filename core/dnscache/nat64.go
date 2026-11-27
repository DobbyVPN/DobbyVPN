package dnscache

import (
	"fmt"
	"net/netip"
)

// SynthesizeNAT64 embeds an IPv4 endpoint in a captured RFC 6052 network prefix.
// The prefix comes from the physical interface, never from a subscription.
func SynthesizeNAT64(prefix netip.Prefix, address netip.Addr) (netip.Addr, error) {
	if !prefix.IsValid() || !prefix.Addr().Is6() || prefix.Addr().Is4In6() || !address.Is4() {
		return netip.Addr{}, fmt.Errorf("invalid NAT64 prefix/address: %s %s", prefix, address)
	}
	switch prefix.Bits() {
	case 32, 40, 48, 56, 64, 96:
	default:
		return netip.Addr{}, fmt.Errorf("unsupported NAT64 prefix length %d", prefix.Bits())
	}
	result := prefix.Masked().Addr().As16()
	position := prefix.Bits() / 8
	for _, value := range address.As4() {
		if position == 8 {
			position++
		} // RFC 6052 reserved u octet.
		result[position] = value
		position++
	}
	return netip.AddrFrom16(result), nil
}
