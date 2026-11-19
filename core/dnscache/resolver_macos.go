//go:build darwin && !(android || ios)

package dnscache

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"syscall"

	"github.com/miekg/dns"
)

const (
	macOSIPBoundIf   = 25  // IP_BOUND_IF
	macOSIPv6BoundIf = 125 // IPV6_BOUND_IF
)

// MacOSLinkResolver queries the captured physical DNS servers from backend
// sockets bound to the physical interface. mDNSResponder is not used: the
// kill switch blocks its port 53 traffic outside the tunnel and admits only
// this backend's queries to these servers.
func MacOSLinkResolver(ipv4, ipv6 int, servers []netip.Addr, prefixes []netip.Prefix) Resolver {
	addresses := make([]string, 0, len(servers))
	for _, server := range servers {
		if server.IsLinkLocalUnicast() && server.Zone() == "" && ipv6 > 0 {
			server = server.WithZone(strconv.Itoa(ipv6))
		}
		addresses = append(addresses, net.JoinHostPort(server.String(), "53"))
	}
	var queryTypes []uint16
	if ipv4 > 0 || ipv6 > 0 {
		// A records can be fetched over an IPv6 resolver transport and then
		// synthesized using the interface's native PREF64 inventory.
		queryTypes = append(queryTypes, dns.TypeA)
	}
	if ipv6 > 0 {
		queryTypes = append(queryTypes, dns.TypeAAAA)
	}
	dialer := &net.Dialer{Control: func(network, _ string, raw syscall.RawConn) error {
		level, option, index := syscall.IPPROTO_IP, macOSIPBoundIf, ipv4
		if network == "udp6" || network == "tcp6" {
			level, option, index = syscall.IPPROTO_IPV6, macOSIPv6BoundIf, ipv6
		}
		if index <= 0 {
			return fmt.Errorf("no physical interface for DNS socket network %q", network)
		}
		var bindErr error
		if err := raw.Control(func(fd uintptr) { bindErr = syscall.SetsockoptInt(int(fd), level, option, index) }); err != nil {
			return err
		}
		return bindErr
	}}
	name := fmt.Sprintf("macos:%d:%d", ipv4, ipv6)
	return func(ctx context.Context, host string) ([]net.IPAddr, error) {
		replies, err := resolveCapturedDNSServers(ctx, host, addresses, dialer, name, queryTypes)
		if err != nil || ipv4 > 0 {
			return replies, err
		}
		var result []net.IPAddr
		for _, reply := range replies {
			address, ok := netip.AddrFromSlice(reply.IP)
			if !ok {
				continue
			}
			address = address.Unmap()
			if address.Is6() {
				result = append(result, reply)
				continue
			}
			for _, prefix := range prefixes {
				synthesized, synthErr := SynthesizeNAT64(prefix, address)
				if synthErr != nil {
					return nil, synthErr
				}
				result = append(result, net.IPAddr{IP: net.IP(synthesized.AsSlice())})
			}
		}
		if len(result) == 0 {
			return nil, fmt.Errorf("no usable IPv6 or NAT64 endpoint for %s", host)
		}
		return result, nil
	}
}
