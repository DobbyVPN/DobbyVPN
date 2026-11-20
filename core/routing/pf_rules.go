package routing

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

// MacOSPFAnchor is evaluated by the stock `anchor "com.apple/*"` rule of
// /etc/pf.conf, so the system's main ruleset is never replaced or flushed.
const MacOSPFAnchor = "com.apple/dobbyvpn"

// PFDestination is one transport destination the backend authorized for an
// attempt. Port 0 admits every port of the address. Bypass marks application
// traffic sent outside the tunnel (ExcludeIPs) rather than the VPN transport.
type PFDestination struct {
	Protocol string // "tcp" or "udp"
	Addr     netip.Addr
	Port     uint16
	Bypass   bool
}

// MacOSPFRules renders the kill switch anchor. Outbound packets leave only
// through loopback and the tunnel, except DHCP, IPv6 neighbor discovery, the
// backend's queries to the physical DNS servers and the backend's authorized
// transport destinations. Application traffic outside the tunnel never gets
// port 53, so DNS cannot bypass the tunnel through ExcludeIPs; the VPN
// transport keeps port 53 even when the server is inside an excluded range.
// Excluded networks (ExcludeIPs) are one table and one rule, so direct traffic
// to them needs no per-connection rule and no reload.
// Loopback and tunnel rules keep no state: connections that existed before
// the anchor loaded continue there without a state entry.
func MacOSPFRules(tunName string, dnsServers []netip.Addr, excluded []netip.Prefix, destinations []PFDestination) string {
	var b strings.Builder
	b.WriteString("# DobbyVPN kill switch: outbound traffic leaves only through the tunnel.\n")
	if len(excluded) > 0 {
		b.WriteString("table <dobbyvpn_excluded> persist {")
		for i, prefix := range excluded {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString("\n\t" + prefix.String())
		}
		b.WriteString("\n}\n")
	}
	b.WriteString("pass out quick on lo0 all no state\n")
	if tunName != "" {
		fmt.Fprintf(&b, "pass out quick on %s all no state\n", tunName)
	}
	b.WriteString("pass out quick inet proto udp from any port 68 to any port 67\n")
	b.WriteString("pass out quick inet6 proto udp from any port 546 to any port 547\n")
	b.WriteString("pass out quick inet6 proto icmp6 all icmp6-type { 133, 135, 136, 143 }\n")
	servers := slices.Clone(dnsServers)
	slices.SortFunc(servers, func(a, b netip.Addr) int { return a.Unmap().WithZone("").Compare(b.Unmap().WithZone("")) })
	for _, server := range slices.Compact(servers) {
		fmt.Fprintf(&b, "pass out quick %s proto { tcp, udp } from any to %s port 53 user root\n", pfFamily(server), pfAddress(server))
	}
	if len(excluded) > 0 {
		b.WriteString("pass out quick proto { tcp, udp } from any to <dobbyvpn_excluded> port != 53 user root\n")
	}
	sorted := slices.Clone(destinations)
	slices.SortFunc(sorted, func(a, b PFDestination) int {
		if c := a.Addr.Unmap().Compare(b.Addr.Unmap()); c != 0 {
			return c
		}
		if c := strings.Compare(a.Protocol, b.Protocol); c != 0 {
			return c
		}
		if a.Port != b.Port {
			return int(a.Port) - int(b.Port)
		}
		if a.Bypass == b.Bypass {
			return 0
		}
		if a.Bypass {
			return 1
		}
		return -1
	})
	for _, d := range slices.Compact(sorted) {
		port := ""
		switch {
		case d.Bypass && d.Port == 53:
			continue
		case d.Bypass && d.Port == 0:
			port = " port != 53"
		case d.Port != 0:
			port = fmt.Sprintf(" port %d", d.Port)
		}
		fmt.Fprintf(&b, "pass out quick %s proto %s from any to %s%s user root\n", pfFamily(d.Addr), d.Protocol, pfAddress(d.Addr), port)
	}
	b.WriteString("block drop out quick all\n")
	return b.String()
}

// MergePFPrefixes returns the networks as sorted prefixes without duplicates
// and without networks nested in another one.
func MergePFPrefixes(networks []*net.IPNet) []netip.Prefix {
	prefixes := make([]netip.Prefix, 0, len(networks))
	for _, network := range networks {
		if network == nil {
			continue
		}
		address, ok := netip.AddrFromSlice(network.IP)
		bits, size := network.Mask.Size()
		if !ok || size == 0 {
			continue
		}
		address = address.Unmap()
		if address.Is4() && size == 128 { // IPv4-mapped IPv6 network
			bits -= 96
		}
		if prefix := netip.PrefixFrom(address, bits).Masked(); prefix.IsValid() {
			prefixes = append(prefixes, prefix)
		}
	}
	slices.SortFunc(prefixes, func(a, b netip.Prefix) int {
		if c := a.Addr().Compare(b.Addr()); c != 0 {
			return c
		}
		return a.Bits() - b.Bits()
	})
	merged := prefixes[:0]
	for _, prefix := range prefixes {
		// CIDR blocks are nested or disjoint; sorted by start, a nested block
		// follows the block that contains it.
		if n := len(merged); n > 0 && merged[n-1].Bits() <= prefix.Bits() && merged[n-1].Contains(prefix.Addr()) {
			continue
		}
		merged = append(merged, prefix)
	}
	return merged
}

func pfFamily(ip netip.Addr) string {
	if ip.Unmap().Is4() {
		return "inet"
	}
	return "inet6"
}

// pf takes no zone in an address; the rule matches the address on any link.
func pfAddress(ip netip.Addr) string { return ip.Unmap().WithZone("").String() }

// PFEvaluatesAnchor reports whether `pfctl -s rules` output of the main
// ruleset contains the stock wildcard anchor that evaluates MacOSPFAnchor.
func PFEvaluatesAnchor(mainRules string) bool {
	for _, line := range strings.Split(mainRules, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), `anchor "com.apple/*"`) {
			return true
		}
	}
	return false
}

// PFEnableToken extracts the reference token printed by `pfctl -E`.
func PFEnableToken(output string) (string, error) {
	for _, line := range strings.Split(output, "\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(name) != "Token" {
			continue
		}
		value = strings.TrimSpace(value)
		if _, err := strconv.ParseUint(value, 10, 64); err != nil {
			return "", fmt.Errorf("invalid pf enable token %q", value)
		}
		return value, nil
	}
	return "", errors.New("pfctl -E printed no enable token")
}
