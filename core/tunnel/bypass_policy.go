package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"

	"core/log"
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

// BypassPolicyLease owns a temporary replacement of the process-wide bypass
// policy. Release is idempotent and restores the exact policy which existed
// before acquisition. Session orchestration is serialized, so leases must be
// released in acquisition order and must not overlap.
type BypassPolicyLease struct {
	once     sync.Once
	previous []*net.IPNet
}

// AcquireBypassPolicy validates and resolves the complete policy before
// changing global state. A failed acquisition therefore leaves the baseline
// policy untouched.
func AcquireBypassPolicy(ctx context.Context, entries []string) (*BypassPolicyLease, error) {
	next, err := resolveRoutingEntries(ctx, entries)
	if err != nil {
		return nil, err
	}
	routesMu.Lock()
	previous := cloneIPNets(defaultBypassCIDRs)
	defaultBypassCIDRs = cloneIPNets(next)
	routesMu.Unlock()
	log.Debugf(Category, "[Routing] Acquired session bypass policy: %v", summarizeCIDRs(next))
	return &BypassPolicyLease{previous: previous}, nil
}

func (l *BypassPolicyLease) Release() {
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

func resolveRoutingEntries(ctx context.Context, entries []string) ([]*net.IPNet, error) {
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
		resolved, err := resolveHostToCIDRs(ctx, entry)
		if err != nil {
			errs = append(errs, fmt.Errorf("resolve bypass host %q: %w", entry, err))
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
	result := make([]string, 0, len(cidrs))
	for _, cidr := range cidrs {
		result = append(result, cidr.String())
	}
	return result
}

func resolveHostToCIDRs(ctx context.Context, host string) ([]*net.IPNet, error) {
	resolver := net.Resolver{}
	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		log.Debugf(Category, "[Bypass] resolve failed for %s: %v", host, err)
		return nil, err
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
	if len(result) == 0 {
		return nil, errors.New("resolver returned no IP addresses")
	}
	return result, nil
}
