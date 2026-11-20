package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"

	"core/iprange"
	"core/log"
)

// BypassPolicy is resolved once for one runtime attempt and is immutable after
// construction. The configuration's ranges are shared by every attempt and
// profile; only host names and protocol upstreams are resolved per attempt.
type BypassPolicy struct {
	excluded  *iprange.Set // the configuration's ExcludeIPs ranges
	hosts     *iprange.Set // addresses of the configuration's host names
	upstreams *iprange.Set // numeric protocol upstream addresses
	// ipv6Unavailable reports, without blocking, that the attempt's transport
	// cannot reach IPv6 destinations. Nil means IPv6 is never refused.
	ipv6Unavailable func() bool
}

// WithIPv6Availability returns a copy that refuses IPv6 connections while
// unavailable reports true.
func (p *BypassPolicy) WithIPv6Availability(unavailable func() bool) *BypassPolicy {
	policy := *p
	policy.ipv6Unavailable = unavailable
	return &policy
}

// Excluded returns the user's excluded addresses, the configuration's ranges
// and its resolved host names, without the protocol upstream addresses.
func (p *BypassPolicy) Excluded() *iprange.Set {
	if p == nil {
		return nil
	}
	if p.hosts.Len() == 0 {
		return p.excluded
	}
	return iprange.New(append(p.excluded.Ranges(), p.hosts.Ranges()...))
}

func (p *BypassPolicy) IsBypass(metadata *M.Metadata) bool {
	if p == nil || metadata == nil {
		return false
	}
	destination := metadata.DstIP
	return p.excluded.Contains(destination) || p.hosts.Contains(destination) || p.upstreams.Contains(destination)
}

// ResolveBypassPolicy resolves the host names before returning a value for
// this runtime attempt.
func ResolveBypassPolicy(ctx context.Context, excluded *iprange.Set, hosts []string) (*BypassPolicy, error) {
	return ResolveBypassPolicyWithResolver(ctx, excluded, hosts, net.DefaultResolver.LookupIPAddr)
}

func ResolveBypassPolicyWithResolver(ctx context.Context, excluded *iprange.Set, hosts []string, resolve func(context.Context, string) ([]net.IPAddr, error)) (*BypassPolicy, error) {
	var resolved []iprange.Range
	for _, host := range hosts {
		addresses, err := resolveHostWithResolver(ctx, host, resolve)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, fmt.Errorf("resolve bypass host: %w", ctxErr)
			}
			// An ExcludeIPs mistake must not stop the VPN, so the host's
			// traffic stays in the tunnel. The name is not logged.
			log.Warn(Category, "ExcludeIPs: a host name could not be resolved and is ignored; its traffic stays in the VPN", nil)
			continue
		}
		resolved = append(resolved, addresses...)
	}
	policy := &BypassPolicy{excluded: excluded, hosts: iprange.New(resolved)}
	policy.recordResolved()
	return policy, nil
}

// WithNumericIPs returns a policy copy that also bypasses only the supplied
// numeric addresses. Hostnames and CIDR strings are deliberately not accepted.
func (p *BypassPolicy) WithNumericIPs(ips []net.IP) (*BypassPolicy, error) {
	if p == nil {
		return nil, errors.New("cannot copy a nil bypass policy")
	}
	upstreams := p.upstreams.Ranges()
	for index, ip := range ips {
		address, ok := netip.AddrFromSlice(ip)
		if !ok {
			return nil, fmt.Errorf("numeric bypass address[%d] is invalid", index)
		}
		upstreams = append(upstreams, iprange.Range{First: address.Unmap(), Last: address.Unmap()})
	}
	policy := *p
	policy.upstreams = iprange.New(upstreams)
	policy.recordResolved()
	return &policy, nil
}

// recordResolved logs counts and a hash of the large configuration list and
// the few per-attempt addresses.
func (p *BypassPolicy) recordResolved() {
	var upstreams []string
	for _, r := range p.upstreams.Ranges() {
		upstreams = append(upstreams, r.First.String()+"-"+r.Last.String())
	}
	detail := map[string]any{
		"exclude_ranges":        p.excluded.Len(),
		"exclude_sha256":        p.excluded.Hash(),
		"host_address_ranges":   p.hosts.Len(),
		"protocol_upstream_ips": upstreams,
	}
	if err := log.SetPolicyDetail("resolved_bypass", detail); err != nil {
		log.Errorf(Category, "retain resolved bypass policy: %v", err)
	}
}

func resolveHostWithResolver(ctx context.Context, host string, resolve func(context.Context, string) ([]net.IPAddr, error)) ([]iprange.Range, error) {
	if resolve == nil {
		return nil, errors.New("underlying bypass resolver is required")
	}
	ips, err := resolve(ctx, host)
	if err != nil {
		log.Debugf(Category, "[Bypass] resolve failed for %s: %v", host, err)
		return nil, err
	}
	var result []iprange.Range
	for _, ip := range ips {
		address, ok := netip.AddrFromSlice(ip.IP)
		if !ok {
			return nil, fmt.Errorf("resolver returned invalid address %v", ip)
		}
		result = append(result, iprange.Range{First: address.Unmap(), Last: address.Unmap()})
	}
	if len(result) == 0 {
		return nil, errors.New("resolver returned no IP addresses")
	}
	log.Debug(Category, "bypass host resolved", map[string]any{"host": host, "addresses": len(result)})
	return result, nil
}
