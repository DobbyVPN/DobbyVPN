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

// StaleAnswerLimit bounds how old a remembered answer may be when it is served
// because the physical network's DNS failed.
const StaleAnswerLimit = 24 * time.Hour

// now is a test seam for answer ages.
var now = time.Now

// Resolver is scoped to the physical network. Its native resolver or captured
// DNS servers own caching and DNS64 answers; Dobby must not pin another CDN TTL.
// The only exception is serve-stale on resolver failure (RFC 8767): when a
// query fails with an error or timeout, the last successful answer for the
// same network and host may be used, at most StaleAnswerLimit old. With
// working DNS every lookup still goes to the resolver.
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
	// lastGood holds the last successful answer per network identity and
	// host, used only when a later query on the same network fails.
	lastGood map[staleKey]staleAnswer
}

type staleKey struct{ identity, host string }

type staleAnswer struct {
	addresses []net.IPAddr
	at        time.Time
	warned    bool // the current failure episode is already logged
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
	parent := ctx
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
	// A query that failed, as opposed to an empty or negative answer.
	var dnsErr *net.DNSError
	queryFailed := (err != nil && !(errors.As(err, &dnsErr) && dnsErr.IsNotFound)) || ctx.Err() != nil
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
	key := staleKey{identity, host}
	if err != nil {
		// Only a failed query on the same network may use the last answer: not
		// a negative answer, a replaced network or a caller that gave up.
		if queryFailed && current == revision && parent.Err() == nil && c.sameNetwork(parent, source, identity) {
			if stale, ok := c.staleAnswer(key, err); ok {
				return stale, nil
			}
		}
		return nil, fmt.Errorf("resolve endpoint %s: %w", host, err)
	}
	c.mu.Lock()
	if c.lastGood == nil {
		c.lastGood = make(map[staleKey]staleAnswer)
	}
	for k, answer := range c.lastGood {
		if now().Sub(answer.at) > StaleAnswerLimit {
			delete(c.lastGood, k)
		}
	}
	c.lastGood[key] = staleAnswer{addresses: append([]net.IPAddr(nil), addresses...), at: now()}
	c.mu.Unlock()
	return addresses, nil
}

// sameNetwork recaptures the physical network after a failed query: a native
// resolver may fail because the network was replaced before the cache's
// revision changed, and an answer from the old network must not be used.
func (c *Cache) sameNetwork(ctx context.Context, source ResolverSource, identity string) bool {
	if source == nil {
		return true
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	latest, _, err := source(ctx)
	return err == nil && latest == identity
}

// staleAnswer returns the last answer for key if it is recent enough. The
// first use in a failure episode is logged once; a successful lookup ends it.
func (c *Cache) staleAnswer(key staleKey, cause error) ([]net.IPAddr, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	answer, ok := c.lastGood[key]
	age := now().Sub(answer.at)
	if !ok || age > StaleAnswerLimit {
		return nil, false
	}
	if !answer.warned {
		answer.warned = true
		c.lastGood[key] = answer
		log.Warn(Category, "underlying DNS failed; using the last known endpoint address", map[string]any{
			"host": key.host, "answer_age_ms": age.Milliseconds(), "cause": cause.Error(),
		})
	}
	return append([]net.IPAddr(nil), answer.addresses...), true
}

func NormalizeHost(host string) string {
	host = strings.TrimSpace(host)
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	if _, err := netip.ParseAddr(host); err == nil {
		return host
	}
	return strings.TrimSuffix(strings.ToLower(host), ".")
}
