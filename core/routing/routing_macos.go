//go:build darwin && !(android || ios)

package routing

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sync/atomic"
	"time"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

var ipv6DefaultSubnets = []string{ipv6LowerHalf, ipv6UpperHalf}
var macOSRouteSequence atomic.Int32
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
	return &route.RouteMessage{Version: unix.RTM_VERSION, Type: operation, Flags: r.flags, Index: r.index, ID: uintptr(os.Getpid()), Seq: int(macOSRouteSequence.Add(1)), Addrs: addresses}
}

func changeMacOSRoute(ctx context.Context, operation int, r macOSRoute) (resultErr error) {
	message := r.message(operation)
	data, err := message.Marshal()
	if err != nil {
		return err
	}
	fd, err := unix.Socket(unix.AF_ROUTE, unix.SOCK_RAW, unix.AF_UNSPEC)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, unix.Close(fd)) }()
	if err := unix.SetNonblock(fd, true); err != nil {
		return err
	}
	limit, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, err := unix.Write(fd, data); err != nil {
		return fmt.Errorf("write route operation %d: %w", operation, err)
	}
	// Each operation has its own socket and sequence. Unrelated notifications
	// cannot acknowledge this owner's mutation.
	buffer := make([]byte, 64*1024)
	for {
		if err := limit.Err(); err != nil {
			return fmt.Errorf("route operation %d acknowledgement: %w", operation, err)
		}
		n, err := unix.Read(fd, buffer)
		if errors.Is(err, unix.EAGAIN) {
			select {
			case <-limit.Done():
			case <-time.After(10 * time.Millisecond):
			}
			continue
		}
		if err != nil {
			return err
		}
		messages, err := route.ParseRIB(route.RIBTypeRoute, buffer[:n])
		if err != nil {
			return err
		}
		for _, value := range messages {
			ack, ok := value.(*route.RouteMessage)
			if ok && ack.ID == message.ID && ack.Seq == message.Seq {
				return ack.Err
			}
		}
	}
}
