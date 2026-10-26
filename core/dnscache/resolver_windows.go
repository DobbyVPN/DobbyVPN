//go:build windows && (amd64 || arm64)

package dnscache

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"net"
	"net/netip"
	"runtime"
	"strconv"
	"syscall"
	"unsafe"

	"core/log"

	"golang.org/x/sys/windows"
)

type windowsResolver struct {
	index     uint32
	ipv4Index uint32
	ipv6Index uint32
	identity  string
	servers   []net.IPAddr
}

func (c *Cache) UseInterface(index int) error {
	if _, _, _, err := CaptureWindowsInterface(context.Background(), index); err != nil {
		return err
	}
	c.SetResolverSource(func(ctx context.Context) (string, Resolver, error) {
		identity, _, resolver, err := CaptureWindowsInterface(ctx, index)
		return identity, resolver, err
	})
	return nil
}

// CaptureWindowsInterface freezes an interface's native resolver configuration.
// Each answer is fenced against changes to both its addresses and DNS servers.
func CaptureWindowsInterface(ctx context.Context, index int) (string, []netip.Addr, Resolver, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, nil, err
	}
	if index <= 0 || uint64(index) > math.MaxUint32 {
		return "", nil, nil, fmt.Errorf("invalid underlying interface index %d", index)
	}
	resolver, err := captureWindowsResolver(uint32(index))
	if err != nil {
		return "", nil, nil, err
	}
	var servers []netip.Addr
	for _, server := range resolver.servers {
		ip, ok := netip.AddrFromSlice(server.IP)
		if !ok {
			return "", nil, nil, errors.New("invalid native DNS address")
		}
		servers = append(servers, ip.Unmap().WithZone(server.Zone))
	}
	lookup := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		before, err := captureWindowsResolver(resolver.index)
		if err != nil {
			return nil, err
		}
		if before.identity != resolver.identity {
			return nil, errors.New("underlying DNS configuration changed before lookup")
		}
		addresses, err := resolver.lookup(ctx, host)
		after, inspectErr := captureWindowsResolver(resolver.index)
		if inspectErr != nil {
			return nil, errors.Join(err, inspectErr)
		}
		if after.identity != resolver.identity {
			return nil, errors.Join(err, errors.New("underlying DNS changed during lookup"))
		}
		return addresses, err
	}
	return resolver.identity, servers, lookup, nil
}

func captureWindowsResolver(index uint32) (*windowsResolver, error) {
	size := uint32(15 * 1024)
	for {
		buffer := make([]byte, size)
		first := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buffer[0]))
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, 0, 0, first, &size)
		if errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for adapter := first; adapter != nil; adapter = adapter.Next {
			if adapter.IfIndex != index && adapter.Ipv6IfIndex != index {
				continue
			}
			resolver := &windowsResolver{index: index, ipv4Index: adapter.IfIndex, ipv6Index: adapter.Ipv6IfIndex}
			var local []string
			for address := adapter.FirstUnicastAddress; address != nil; address = address.Next {
				local = append(local, address.Address.IP().String())
			}
			for server := adapter.FirstDnsServerAddress; server != nil; server = server.Next {
				if server.Address.Sockaddr == nil {
					continue
				}
				ip := server.Address.IP()
				if ip == nil || ip.IsUnspecified() || ip.IsLoopback() {
					continue
				}
				address := net.IPAddr{IP: append(net.IP(nil), ip...)}
				if ip.To4() == nil && ip.IsLinkLocalUnicast() {
					address.Zone = strconv.FormatUint(uint64(adapter.Ipv6IfIndex), 10)
				}
				resolver.servers = append(resolver.servers, address)
			}
			if len(resolver.servers) == 0 {
				return nil, errors.New("physical interface has no non-loopback DNS servers")
			}
			resolver.identity = fmt.Sprintf("windows:%d:%d:%d:%d:%v:%v", index, adapter.Luid, adapter.IfIndex, adapter.Ipv6IfIndex, local, resolver.servers)
			runtime.KeepAlive(buffer)
			return resolver, nil
		}
		return nil, fmt.Errorf("underlying DNS interface %d is unavailable", index)
	}
}

// The shared DNS Client service sends DnsQueryEx traffic under its own identity.
// Query from backend-owned sockets so the retained guard can authorize only this
// resolver's captured servers, without permitting other applications' DNS.
func (r *windowsResolver) lookup(ctx context.Context, host string) ([]net.IPAddr, error) {
	servers := make([]string, 0, len(r.servers))
	for _, server := range r.servers {
		servers = append(servers, net.JoinHostPort(server.String(), "53"))
	}
	dialer := &net.Dialer{Control: func(network, address string, raw syscall.RawConn) error {
		level := syscall.IPPROTO_IPV6
		index := r.ipv6Index
		switch network {
		case "udp4", "tcp4":
			level = syscall.IPPROTO_IP
			index = r.ipv4Index
		case "udp6", "tcp6":
		default:
			return fmt.Errorf("unsupported physical DNS socket network %q", network)
		}
		if index == 0 {
			return fmt.Errorf("physical DNS interface %d has no %s index", r.index, network)
		}
		selectedIndex := index
		if level == syscall.IPPROTO_IP {
			index = bits.ReverseBytes32(index)
		}
		var bindErr error
		if err := raw.Control(func(fd uintptr) {
			bindErr = syscall.SetsockoptInt(syscall.Handle(fd), level, 31, int(index))
			log.Debugf(Category, "Windows endpoint DNS socket interface=%d network=%s server=%s fd=%d err=%v", selectedIndex, network, address, fd, bindErr)
		}); err != nil {
			return err
		}
		return bindErr
	}}
	addresses, err := resolveCapturedDNSServers(ctx, host, servers, dialer, fmt.Sprintf("windows:%d", r.index))
	for i := range addresses {
		if addresses[i].IP.To4() == nil && addresses[i].IP.IsLinkLocalUnicast() {
			addresses[i].Zone = strconv.FormatUint(uint64(r.ipv6Index), 10)
		}
	}
	return addresses, err
}
