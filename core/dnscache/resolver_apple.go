//go:build (darwin || ios) && cgo

package dnscache

/*
// DNSService APIs are exported by Darwin's libSystem, which the linker includes.
#include <dns_sd.h>
#include <netinet/in.h>
#include <stdlib.h>
#include <string.h>

typedef struct dobby_dns_address {
 unsigned char bytes[16];
 unsigned int length;
 unsigned int scope;
 struct dobby_dns_address *next;
} dobby_dns_address;
typedef struct {
 DNSServiceRef service;
 dobby_dns_address *addresses;
 dobby_dns_address **tail;
 int complete;
 DNSServiceErrorType error;
} dobby_dns_query;

static void dobby_dns_reply(DNSServiceRef service, DNSServiceFlags flags, uint32_t interfaceIndex,
 DNSServiceErrorType error, const char *hostname, const struct sockaddr *address, uint32_t ttl, void *context) {
 dobby_dns_query *query = context;
 if (error == kDNSServiceErr_NoSuchRecord) { query->complete = 1; return; }
 if (error != kDNSServiceErr_NoError) { query->error = error; query->complete = 1; return; }
 if (!(flags & kDNSServiceFlagsAdd) || address == NULL) return;
 dobby_dns_address *item = calloc(1, sizeof(*item));
 if (item == NULL) { query->error = kDNSServiceErr_NoMemory; query->complete = 1; return; }
 if (address->sa_family == AF_INET) {
  item->length = 4;
  memcpy(item->bytes, &((const struct sockaddr_in *)address)->sin_addr, 4);
 } else if (address->sa_family == AF_INET6) {
  const struct sockaddr_in6 *ipv6 = (const struct sockaddr_in6 *)address;
  item->length = 16;
  memcpy(item->bytes, &ipv6->sin6_addr, 16);
  item->scope = ipv6->sin6_scope_id;
  if (item->scope == 0 && IN6_IS_ADDR_LINKLOCAL(&ipv6->sin6_addr)) item->scope = interfaceIndex;
 } else { free(item); query->error = kDNSServiceErr_BadParam; query->complete = 1; return; }
 *query->tail = item;
 query->tail = &item->next;
 if (!(flags & kDNSServiceFlagsMoreComing)) query->complete = 1;
}
static dobby_dns_query *dobby_dns_start(const char *host, unsigned int index, unsigned int protocol) {
 dobby_dns_query *query = calloc(1,sizeof(*query));
 if (query == NULL) return NULL;
 query->tail = &query->addresses;
 // Negative A/AAAA answers otherwise leave the subscription open indefinitely.
 // GetAddrInfo reports address results only, even with intermediate replies on.
 query->error = DNSServiceGetAddrInfo(&query->service,kDNSServiceFlagsReturnIntermediates,index,protocol,host,dobby_dns_reply,query);
 return query;
}
static void dobby_dns_close(dobby_dns_query *query) {
 if (query->service != NULL) DNSServiceRefDeallocate(query->service);
 while (query->addresses != NULL) {
  dobby_dns_address *next = query->addresses->next;
  free(query->addresses);
  query->addresses = next;
 }
 free(query);
}
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"strconv"
	"time"
	"unsafe"

	"core/log"

	"golang.org/x/sys/unix"
)

// UseInterface retains a physical interface, never the VPN's default DNS
// domain. DNSServiceGetAddrInfo keeps native caching and DNS64 synthesis.
func (c *Cache) UseInterface(index int) error {
	if index <= 0 || index > math.MaxUint32 {
		return fmt.Errorf("invalid underlying interface index %d", index)
	}
	iface, err := net.InterfaceByIndex(index)
	if err != nil {
		return err
	}
	addresses, err := iface.Addrs()
	if err != nil {
		return err
	}
	c.SetResolver(fmt.Sprintf("apple:%s:%d:%v", iface.Name, index, addresses), func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return resolveAppleLink(ctx, index, host)
	})
	return nil
}

// AppleLinkResolver uses the native resolver scoped to a physical interface.
// Its network owner supplies the before/after revision fence.
func AppleLinkResolver(index int) Resolver {
	return func(ctx context.Context, host string) ([]net.IPAddr, error) {
		if index <= 0 || uint64(index) > math.MaxUint32 {
			return nil, fmt.Errorf("invalid physical interface index %d", index)
		}
		return resolveAppleLink(ctx, index, host)
	}
}

func resolveAppleLink(ctx context.Context, index int, host string) ([]net.IPAddr, error) {
	type response struct {
		addresses []net.IPAddr
		err       error
	}
	results := make(chan response, 2)
	// Each family completes independently, including negative answers. Waiting
	// for both prevents a fast A reply from hiding a usable IPv6/DNS64 path.
	for _, protocol := range []C.uint{C.kDNSServiceProtocol_IPv6, C.kDNSServiceProtocol_IPv4} {
		go func(protocol C.uint) {
			started := time.Now()
			addresses, err := queryAppleLink(ctx, index, host, protocol)
			log.Debugf(Category, "native Apple lookup interface=%d host=%s protocol=%d addresses=%v elapsed=%s err=%v", index, host, uint32(protocol), addresses, time.Since(started), err)
			if err != nil {
				err = fmt.Errorf("native Apple DNS interface=%d protocol=%d host=%s: %w", index, uint32(protocol), host, err)
			}
			results <- response{addresses, err}
		}(protocol)
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

func queryAppleLink(ctx context.Context, index int, host string, protocol C.uint) ([]net.IPAddr, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name := C.CString(host)
	defer C.free(unsafe.Pointer(name))
	query := C.dobby_dns_start(name, C.uint(index), protocol)
	if query == nil {
		return nil, errors.New("allocate native DNS query")
	}
	defer C.dobby_dns_close(query)
	if query.error != 0 {
		return nil, fmt.Errorf("DNSServiceGetAddrInfo interface=%d: %d", index, int(query.error))
	}
	fd := C.DNSServiceRefSockFD(query.service)
	if fd < 0 {
		return nil, errors.New("native DNS query has no descriptor")
	}
	poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	for query.complete == 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// This polling interval only observes cancellation; it never determines
		// lookup success or extends the caller's deadline.
		ready, err := unix.Poll(poll, 50)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if ready == 0 {
			continue
		}
		if poll[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			return nil, errors.New("native DNS query disconnected")
		}
		if code := C.DNSServiceProcessResult(query.service); code != 0 {
			return nil, fmt.Errorf("DNSServiceProcessResult: %d", int(code))
		}
	}
	if query.error != 0 {
		return nil, fmt.Errorf("native DNS reply interface=%d: %d", index, int(query.error))
	}
	var addresses []net.IPAddr
	for item := query.addresses; item != nil; item = item.next {
		ip := net.IP(C.GoBytes(unsafe.Pointer(&item.bytes[0]), C.int(item.length)))
		address := net.IPAddr{IP: ip}
		if item.scope != 0 {
			address.Zone = strconv.FormatUint(uint64(item.scope), 10)
		}
		addresses = append(addresses, address)
	}
	return addresses, nil
}
