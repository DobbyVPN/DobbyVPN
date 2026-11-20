//go:build darwin && !(android || ios)

package dnscache

import (
	"context"
	"fmt"
	"net"
	"net/netip"
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
func MacOSLinkResolver(ipv4, ipv6 int, servers []netip.Addr) Resolver {
	addresses := make([]string, 0, len(servers))
	for _, server := range servers {
		addresses = append(addresses, net.JoinHostPort(server.String(), "53"))
	}
	var queryTypes []uint16
	if ipv4 > 0 {
		queryTypes = append(queryTypes, dns.TypeA)
	}
	if ipv6 > 0 {
		queryTypes = append(queryTypes, dns.TypeAAAA)
	}
	dialer := &net.Dialer{Control: func(network, address string, raw syscall.RawConn) error {
		if destination, err := netip.ParseAddrPort(address); err == nil && destination.Addr().IsLoopback() {
			return nil // a local DNS proxy is reached over loopback, not the physical link
		}
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
		return resolveCapturedDNSServers(ctx, host, addresses, dialer, name, queryTypes)
	}
}
