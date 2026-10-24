package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"

	"core/log"
)

// BypassPolicy is resolved once for one runtime attempt and is immutable after
// construction.
type BypassPolicy struct {
	cidrs      []*net.IPNet
	entryCount int
}

func (p *BypassPolicy) IsBypass(metadata *M.Metadata) bool {
	if p == nil {
		return false
	}
	if metadata == nil {
		return false
	}

	destIP := metadata.DstIP
	if !destIP.IsValid() {
		return false
	}

	stdIP := net.IP(destIP.AsSlice())

	for _, route := range p.cidrs {
		if route.Contains(stdIP) {
			log.Debugf(Category, "[Router] BYPASS hit for IP: %s", stdIP)
			return true
		}
	}
	log.Debugf(Category, "[Router] PROXY route for IP: %s", stdIP)
	return false
}

// ResolveBypassPolicy validates and resolves every entry before returning a
// value for this runtime attempt.
func ResolveBypassPolicy(ctx context.Context, entries []string) (*BypassPolicy, error) {
	cidrs, err := resolveRoutingEntries(ctx, entries)
	if err != nil {
		return nil, err
	}
	policy := &BypassPolicy{cidrs: cidrs, entryCount: len(entries)}
	policy.recordResolved()
	return policy, nil
}

// WithNumericIPs returns a policy copy that also bypasses only the supplied
// numeric addresses. Hostnames and CIDR strings are deliberately not accepted.
func (p *BypassPolicy) WithNumericIPs(ips []net.IP) (*BypassPolicy, error) {
	if p == nil {
		return nil, errors.New("cannot copy a nil bypass policy")
	}
	cidrs := append([]*net.IPNet(nil), p.cidrs...)
	for index, ip := range ips {
		var normalized net.IP
		var mask net.IPMask
		if ipv4 := ip.To4(); ipv4 != nil {
			normalized = append(net.IP(nil), ipv4...)
			mask = net.CIDRMask(32, 32)
		} else if ipv6 := ip.To16(); ipv6 != nil {
			normalized = append(net.IP(nil), ipv6...)
			mask = net.CIDRMask(128, 128)
		} else {
			return nil, fmt.Errorf("numeric bypass address[%d] is invalid", index)
		}
		cidrs = append(cidrs, &net.IPNet{IP: normalized, Mask: mask})
	}
	policy := &BypassPolicy{
		cidrs:      cidrs,
		entryCount: p.entryCount,
	}
	policy.recordResolved()
	return policy, nil
}

func (p *BypassPolicy) recordResolved() {
	if err := log.SetPolicyDetail("resolved_bypass_cidrs", summarizeCIDRs(p.cidrs)); err != nil {
		log.Errorf(Category, "retain resolved bypass policy: %v", err)
	}
	log.Debug(Category, "attempt bypass policy resolved", map[string]any{
		"entry_count":         p.entryCount,
		"resolved_cidr_count": len(p.cidrs),
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
	log.Debug(Category, "bypass host resolved", map[string]any{"host": host, "cidrs": summarizeCIDRs(result)})
	return result, nil
}
