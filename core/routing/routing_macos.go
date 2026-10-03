//go:build darwin && !(android || ios)

package routing

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

var ipv6DefaultSubnets = []string{ipv6LowerHalf, ipv6UpperHalf}
var macOSReadRoutes = readMacOSRoutes
var macOSChangeRoute = changeMacOSRoute
var macOSInterface = net.InterfaceByName

type macOSRoute struct {
	prefix  netip.Prefix
	gateway netip.Addr // Invalid for an interface route.
	index   int
	flags   int
}

func isLoopbackIP(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.IsLoopback()
}

func routeIP(address route.Addr) netip.Addr {
	switch a := address.(type) {
	case *route.Inet4Addr:
		return netip.AddrFrom4(a.IP)
	case *route.Inet6Addr:
		return netip.AddrFrom16(a.IP)
	default:
		return netip.Addr{}
	}
}

func routeAddress(ip netip.Addr) route.Addr {
	if ip.Is4() {
		return &route.Inet4Addr{IP: ip.As4()}
	}
	return &route.Inet6Addr{IP: ip.As16()}
}

func readMacOSRoutes() ([]macOSRoute, error) {
	data, err := route.FetchRIB(unix.AF_UNSPEC, route.RIBTypeRoute, 0)
	if err != nil {
		return nil, err
	}
	messages, err := route.ParseRIB(route.RIBTypeRoute, data)
	if err != nil {
		return nil, err
	}
	var result []macOSRoute
	for _, message := range messages {
		r, ok := message.(*route.RouteMessage)
		if !ok || len(r.Addrs) <= unix.RTAX_NETMASK {
			continue
		}
		destination := routeIP(r.Addrs[unix.RTAX_DST])
		if !destination.IsValid() {
			continue
		}
		bits := destination.BitLen()
		if r.Flags&unix.RTF_HOST == 0 {
			mask := routeIP(r.Addrs[unix.RTAX_NETMASK])
			if !mask.IsValid() {
				bits = 0
			} else {
				bits, _ = net.IPMask(mask.AsSlice()).Size()
			}
		}
		result = append(result, macOSRoute{netip.PrefixFrom(destination, bits).Masked(), routeIP(r.Addrs[unix.RTAX_GATEWAY]), r.Index, r.Flags})
	}
	return result, nil
}

const macOSIdentityFlags = unix.RTF_GATEWAY | unix.RTF_HOST | unix.RTF_REJECT | unix.RTF_BLACKHOLE | unix.RTF_STATIC | unix.RTF_IFSCOPE | unix.RTF_PROTO1

func (r macOSRoute) same(other macOSRoute) bool {
	return r.prefix == other.prefix && r.gateway == other.gateway && r.index == other.index && r.flags&macOSIdentityFlags == other.flags&macOSIdentityFlags
}

func (r macOSRoute) message(operation int) *route.RouteMessage {
	addresses := make([]route.Addr, unix.RTAX_MAX)
	addresses[unix.RTAX_DST] = routeAddress(r.prefix.Addr())
	if r.gateway.IsValid() {
		addresses[unix.RTAX_GATEWAY] = routeAddress(r.gateway)
	} else {
		addresses[unix.RTAX_GATEWAY] = &route.LinkAddr{Index: r.index}
	}
	mask, _ := netip.AddrFromSlice(net.CIDRMask(r.prefix.Bits(), r.prefix.Addr().BitLen()))
	addresses[unix.RTAX_NETMASK] = routeAddress(mask)
	addresses[unix.RTAX_IFP] = &route.LinkAddr{Index: r.index}
	return &route.RouteMessage{Version: unix.RTM_VERSION, Type: operation, Flags: r.flags, Index: r.index, ID: uintptr(os.Getpid()), Seq: 1, Addrs: addresses}
}

// Route sockets are atomic: XNU route_output returns the mutation error to
// write(2), even when its notification is lost. A complete successful write
// supplies acquisition evidence; table inspection verifies the exact identity.
// https://github.com/apple-oss-distributions/xnu/blob/main/bsd/net/rtsock.c
func changeMacOSRoute(ctx context.Context, operation int, r macOSRoute) (applied bool, resultErr error) {
	data, err := r.message(operation).Marshal()
	if err != nil {
		return false, err
	}
	fd, err := unix.Socket(unix.AF_ROUTE, unix.SOCK_RAW, unix.AF_UNSPEC)
	if err != nil {
		return false, err
	}
	defer func() { resultErr = errors.Join(resultErr, unix.Close(fd)) }()
	if err := unix.SetNonblock(fd, true); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	count, err := unix.Write(fd, data)
	if err != nil {
		return false, fmt.Errorf("write route operation %d: %w", operation, err)
	}
	if count != len(data) {
		return false, fmt.Errorf("incomplete atomic route write: %d of %d bytes", count, len(data))
	}
	return true, nil
}
