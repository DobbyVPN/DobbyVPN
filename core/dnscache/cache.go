// Package dnscache keeps endpoint resolution separate from VPN application DNS.
package dnscache

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"core/log"
)

const Category = "DNSCache"
const ServerResolveTimeout = 5 * time.Second

// Resolver is scoped to the physical network. Its native resolver or captured
// DNS servers own caching and DNS64 answers; Dobby must not pin another CDN TTL.
type Resolver func(context.Context, string) ([]net.IPAddr, error)

// ResolverSource captures a current physical network before each lookup. The
// returned resolver must reject a result if that native network changes.
type ResolverSource func(context.Context) (string, Resolver, error)

// Cache retains resolver ownership, not a second DNS TTL policy. A revision
// prevents a late lookup on a replaced physical network from being used.
type Cache struct {
	mu       sync.RWMutex
	resolver Resolver
	identity string
	revision uint64
	source   ResolverSource
}

func New() *Cache { return &Cache{revision: 1} }

func (c *Cache) SetResolverSource(source ResolverSource) {
	c.mu.Lock()
	c.source = source
	c.resolver = nil
	c.revision++
	c.mu.Unlock()
}

func (c *Cache) SetResolver(identity string, resolver Resolver) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.identity == identity && c.resolver != nil {
		return
	}
	c.identity, c.resolver = identity, resolver
	c.revision++
	log.Infof(Category, "underlying resolver changed network=%s revision=%d", identity, c.revision)
}

func (c *Cache) ResolveAddresses(ctx context.Context, host string) ([]net.IPAddr, error) {
	if c == nil {
		return nil, errors.New("underlying resolver is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	host = NormalizeHost(host)
	if host == "" {
		return nil, errors.New("empty host")
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return []net.IPAddr{{IP: net.IP(ip.AsSlice()), Zone: ip.Zone()}}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, ServerResolveTimeout)
	defer cancel()
	c.mu.RLock()
	source := c.source
	c.mu.RUnlock()
	if source != nil {
		identity, resolver, err := source(ctx)
		if err != nil {
			return nil, fmt.Errorf("capture underlying resolver: %w", err)
		}
		c.SetResolver(identity, resolver)
	}
	c.mu.RLock()
	resolve, revision, identity := c.resolver, c.revision, c.identity
	c.mu.RUnlock()
	if resolve == nil {
		return nil, errors.New("underlying network resolver is unavailable")
	}
	started := time.Now()
	addresses, err := resolve(ctx, host)
	c.mu.RLock()
	current := c.revision
	c.mu.RUnlock()
	if current != revision {
		err = errors.Join(err, errors.New("underlying network changed during resolution"))
	}
	if ctx.Err() != nil {
		err = errors.Join(err, ctx.Err())
	}
	if err == nil && len(addresses) == 0 {
		err = errors.New("underlying resolver returned no addresses")
	}
	log.Debugf(Category, "underlying lookup network=%s revision=%d host=%s addresses=%v elapsed=%s err=%v", identity, revision, host, addresses, time.Since(started), err)
	if err != nil {
		return nil, fmt.Errorf("resolve endpoint %s: %w", host, err)
	}
	return addresses, nil
}

func NormalizeHost(host string) string {
	host = strings.TrimSpace(host)
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	if _, err := netip.ParseAddr(host); err == nil {
		return host
	}
	return strings.TrimSuffix(strings.ToLower(host), ".")
}
