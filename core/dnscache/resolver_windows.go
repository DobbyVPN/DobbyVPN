//go:build windows && (amd64 || arm64)

package dnscache

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"core/log"

	"golang.org/x/sys/windows"
)

// These are the documented 64-bit DNS_QUERY_REQUEST, DNS_QUERY_RESULT and
// DNS_RECORD layouts. Both supported Windows architectures use this ABI.
type dnsQueryRequest struct {
	version    uint32
	name       *uint16
	recordType uint16
	options    uint64
	servers    *byte
	index      uint32
	callback   uintptr
	owner      uintptr
}
type dnsQueryResult struct {
	version  uint32
	status   uint32
	options  uint64
	records  *dnsRecord
	reserved uintptr
}
type dnsRecord struct {
	next       *dnsRecord
	name       *uint16
	recordType uint16
	length     uint16
	flags      uint32
	ttl        uint32
	reserved   uint32
	data       [32]byte
}
type windowsResolver struct {
	index    uint32
	identity string
	servers  []net.IPAddr
}

var dnsAPI = windows.NewLazySystemDLL("dnsapi.dll")
var dnsQueryEx = dnsAPI.NewProc("DnsQueryEx")
var dnsCancelQuery = dnsAPI.NewProc("DnsCancelQuery")
var dnsRecordFree = dnsAPI.NewProc("DnsRecordListFree")
var dnsRequests sync.Map
var dnsRequestID atomic.Uint64
var dnsQueryCallback = windows.NewCallback(func(owner uintptr, _ uintptr) uintptr {
	if value, ok := dnsRequests.Load(owner); ok {
		close(value.(chan struct{}))
	}
	return 0
})

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
			resolver := &windowsResolver{index: index}
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
			resolver.identity = fmt.Sprintf("windows:%d:%d:%v:%v", index, adapter.Luid, local, resolver.servers)
			runtime.KeepAlive(buffer)
			return resolver, nil
		}
		return nil, fmt.Errorf("underlying DNS interface %d is unavailable", index)
	}
}

func (r *windowsResolver) lookup(ctx context.Context, host string) ([]net.IPAddr, error) {
	type response struct {
		addresses []net.IPAddr
		err       error
	}
	results := make(chan response, 2)
	for _, recordType := range []uint16{28, 1} {
		go func(kind uint16) {
			started := time.Now()
			addresses, err := r.query(ctx, host, kind)
			log.Debugf(Category, "native Windows lookup interface=%d host=%s type=%d servers=%v addresses=%v elapsed=%s err=%v", r.index, host, kind, r.servers, addresses, time.Since(started), err)
			if err != nil {
				err = fmt.Errorf("native Windows DNS interface=%d type=%d host=%s: %w", r.index, kind, host, err)
			}
			results <- response{addresses, err}
		}(recordType)
	}
	var addresses []net.IPAddr
	var failures []error
	for range 2 {
		result := <-results
		addresses = append(addresses, result.addresses...)
		failures = append(failures, result.err)
	}
	if ctx.Err() != nil {
		return nil, errors.Join(ctx.Err(), errors.Join(failures...))
	}
	if len(addresses) == 0 {
		return nil, errors.Join(failures...)
	}
	return addresses, nil
}

func (r *windowsResolver) query(ctx context.Context, host string, kind uint16) ([]net.IPAddr, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name, err := windows.UTF16PtrFromString(host)
	if err != nil {
		return nil, err
	}
	owner := uintptr(dnsRequestID.Add(1))
	completed := make(chan struct{})
	dnsRequests.Store(owner, completed)
	defer dnsRequests.Delete(owner)
	// InterfaceIndex selects the physical adapter's native DNS configuration.
	// Passing an explicit server list as well is rejected by DnsQueryEx. The
	// before/after capture fence ensures this configuration still matches the
	// retained resolver and its authorized bootstrap server addresses.
	request := dnsQueryRequest{version: 1, name: name, recordType: kind, index: r.index, callback: dnsQueryCallback, owner: owner}
	result := dnsQueryResult{version: 1}
	var cancellation [4]uint64 // DNS_QUERY_CANCEL is 32 bytes, aligned to 8.
	status, _, _ := dnsQueryEx.Call(uintptr(unsafe.Pointer(&request)), uintptr(unsafe.Pointer(&result)), uintptr(unsafe.Pointer(&cancellation)))
	if status == 9506 { // DNS_REQUEST_PENDING
		select {
		case <-completed:
		case <-ctx.Done():
			cancelled, _, _ := dnsCancelQuery.Call(uintptr(unsafe.Pointer(&cancellation)))
			// DnsCancelQuery requests cancellation; the completion callback owns the
			// final native pointers. Never free or reuse them before it arrives.
			<-completed
			if cancelled != 0 {
				err = fmt.Errorf("DnsCancelQuery: %w", syscall.Errno(cancelled))
			}
		}
	} else if status != 0 {
		err = fmt.Errorf("DnsQueryEx: %w", syscall.Errno(status))
	}
	runtime.KeepAlive(&request)
	runtime.KeepAlive(&cancellation)
	runtime.KeepAlive(r)
	defer func() {
		if result.records != nil {
			dnsRecordFree.Call(uintptr(unsafe.Pointer(result.records)), 1)
		}
	}()
	if ctx.Err() != nil {
		return nil, errors.Join(ctx.Err(), err)
	}
	if result.status == 9501 || result.status == 9003 {
		return nil, nil
	}
	if result.status != 0 {
		return nil, errors.Join(err, fmt.Errorf("DNS type %d on interface %d: %w", kind, r.index, syscall.Errno(result.status)))
	}
	if err != nil {
		return nil, err
	}
	var addresses []net.IPAddr
	for record := result.records; record != nil; record = record.next {
		var ip net.IP
		switch record.recordType {
		case 1:
			ip = append(net.IP(nil), record.data[:4]...)
		case 28:
			ip = append(net.IP(nil), record.data[:16]...)
		default:
			continue
		}
		address := net.IPAddr{IP: ip}
		if ip.To4() == nil && ip.IsLinkLocalUnicast() {
			address.Zone = strconv.FormatUint(uint64(r.index), 10)
		}
		addresses = append(addresses, address)
	}
	return addresses, nil
}
