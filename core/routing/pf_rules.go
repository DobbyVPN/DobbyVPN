package routing

import (
	"core/iprange"
	"errors"
	"fmt"
	"go4.org/netipx"
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
// to them needs no per-connection rule and no reload. The table is not
// persistent: pf deletes it once a reload without excluded networks leaves no
// rule referring to it.
// Loopback and tunnel rules keep no state: connections that existed before
// the anchor loaded continue there without a state entry.
func MacOSPFRules(tunName string, dnsServers []netip.Addr, excluded []netip.Prefix, destinations []PFDestination) string {
	var b strings.Builder
	b.WriteString("# DobbyVPN kill switch: outbound traffic leaves only through the tunnel.\n")
	if len(excluded) > 0 {
		b.WriteString("table <dobbyvpn_excluded> {")
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

// pfPrefixes converts merged inclusive ranges to the CIDRs accepted by pfctl.
func pfPrefixes(configured, hosts *iprange.Set) []netip.Prefix {
	all := configured
	if configured.Len() == 0 {
		all = hosts
	} else if hosts.Len() != 0 {
		all = iprange.New(append(configured.Ranges(), hosts.Ranges()...))
	}
	var prefixes []netip.Prefix
	for _, r := range all.Ranges() {
		prefixes = netipx.IPRangeFrom(r.First, r.Last).AppendPrefixes(prefixes)
	}
	return prefixes
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

// pfHasReference reads pfctl's References table. The process name can contain
// spaces, and matching arbitrary numeric fields could mistake a PID for the
// token, so the token is read from the column the header labels TOKEN rather
// than from a fixed offset. Unknown output is an error, not proof that a
// reference is absent.
//
//nolint:unused // Used on macOS and exercised by portable Torturer tests.
func pfHasReference(output, token string) (bool, error) {
	table := false
	known := false
	column := -1
	for _, line := range strings.Split(output, "\n") {
		text := strings.TrimSpace(line)
		if text == "No pf starter references held" || text == "No pf_enabled references" {
			return false, nil
		}
		if text == "TOKENS:" {
			table = true
			known = true
			continue
		}
		if !table || text == "" {
			continue
		}
		if strings.Contains(line, "Process Name") && strings.Contains(line, "TOKEN") {
			column = strings.Index(line, "TOKEN")
			continue
		}
		if column < 0 || len(line) <= column {
			return false, fmt.Errorf("invalid pf reference row: %s", line)
		}
		fields := strings.Fields(line[column:])
		value := ""
		if len(fields) > 0 {
			value = fields[0]
		}
		if _, err := strconv.ParseUint(value, 10, 64); err != nil {
			return false, fmt.Errorf("invalid pf reference token row: %s", line)
		}
		if value == token {
			return true, nil
		}
	}
	if !known {
		return false, errors.New("pfctl printed no recognizable reference inventory")
	}
	return false, nil
}
