package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"

	"go_module/log"
)

var (
	routesMu           sync.RWMutex
	defaultBypassCIDRs []*net.IPNet
)

func IsBypass(metadata *M.Metadata) bool {
	if metadata == nil {
		return false
	}

	destIP := metadata.DstIP
	if !destIP.IsValid() {
		return false
	}

	routesMu.RLock()
	defer routesMu.RUnlock()

	stdIP := net.IP(destIP.AsSlice())

	for _, route := range defaultBypassCIDRs {
		if route.Contains(stdIP) {
			log.Debugf(Category, "[Router] BYPASS hit for IP: %s", stdIP)
			return true
		}
	}
	log.Debugf(Category, "[Router] PROXY route for IP: %s", stdIP)
	return false
}

// GeoRoutingLease owns a temporary replacement of the process-wide bypass
// policy. Release is idempotent and restores the exact policy which existed
// before acquisition. Session orchestration is serialized, so leases must be
// released in acquisition order and must not overlap.
type GeoRoutingLease struct {
	once     sync.Once
	previous []*net.IPNet
}

// AcquireGeoRoutingConf validates and resolves the complete policy before
// changing global state. A failed acquisition therefore leaves the baseline
// policy untouched.
func AcquireGeoRoutingConf(entries []string) (*GeoRoutingLease, error) {
	next, err := resolveRoutingEntries(entries)
	if err != nil {
		return nil, err
	}
	routesMu.Lock()
	previous := cloneIPNets(defaultBypassCIDRs)
	defaultBypassCIDRs = cloneIPNets(next)
	routesMu.Unlock()
	log.Debugf(Category, "[Routing] Acquired session bypass policy: %v", summarizeCIDRs(next))
	return &GeoRoutingLease{previous: previous}, nil
}

func (l *GeoRoutingLease) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		routesMu.Lock()
		defaultBypassCIDRs = cloneIPNets(l.previous)
		routesMu.Unlock()
		log.Debugf(Category, "[Routing] Restored previous bypass policy")
	})
}

func resolveRoutingEntries(entries []string) ([]*net.IPNet, error) {
	var result []*net.IPNet
	var errs []error
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if _, network, err := net.ParseCIDR(entry); err == nil {
			result = append(result, network)
			continue
		}
		resolved := resolveHostToCIDRs(entry)
		if len(resolved) == 0 {
			errs = append(errs, fmt.Errorf("could not resolve bypass host"))
			continue
		}
		result = append(result, resolved...)
	}
	if len(errs) != 0 {
		return nil, errors.Join(errs...)
	}
	return result, nil
}

func cloneIPNets(input []*net.IPNet) []*net.IPNet {
	result := make([]*net.IPNet, 0, len(input))
	for _, network := range input {
		if network == nil {
			continue
		}
		result = append(result, &net.IPNet{
			IP:   append(net.IP(nil), network.IP...),
			Mask: append(net.IPMask(nil), network.Mask...),
		})
	}
	return result
}

func summarizeCIDRs(cidrs []*net.IPNet) []string {
	const limit = 10

	count := len(cidrs)
	if count > limit {
		count = limit
	}

	result := make([]string, 0, count+1)
	for _, cidr := range cidrs[:count] {
		result = append(result, cidr.String())
	}
	if len(cidrs) > limit {
		result = append(result, "...")
	}
	return result
}

func resolveHostToCIDRs(host string) []*net.IPNet {
	resolver := net.Resolver{}

	ctx := context.Background()
	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		log.Debugf(Category, "[Bypass] resolve failed for %s: %v", host, err)
		return nil
	}

	var result []*net.IPNet
	for _, ip := range ips {
		if v4 := ip.IP.To4(); v4 != nil {
			_, n, _ := net.ParseCIDR(v4.String() + "/32")
			result = append(result, n)
			continue
		}
		_, n, _ := net.ParseCIDR(ip.String() + "/128")
		result = append(result, n)
	}
	return result
}
