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
// because the physical network's DNS failed; staleAnswerEntries bounds how many
// host names are remembered (the client resolves only a few).
const (
	StaleAnswerLimit   = 24 * time.Hour
	staleAnswerEntries = 64
)

// now is a test seam for answer ages.
var now = time.Now

// Resolver is scoped to the physical network. Its native resolver or captured
// DNS servers own caching and DNS64 answers; Dobby must not pin another CDN TTL.
// The only exception is serve-stale on resolver failure (RFC 8767): when a
// query fails with an error or timeout, the last successful answer for the
// host from the same resolver revision may be used, at most StaleAnswerLimit
// old. Any resolver or network change starts a new revision, so an answer is
// never used on another network. With working DNS every lookup still goes to
// the resolver.
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
	// lastGood holds the last successful answer per host, used only when a
	// later query in the same revision fails.
	lastGood map[string]staleAnswer
}

type staleAnswer struct {
	addresses []net.IPAddr
	revision  uint64
	at        time.Time
	warned    bool // the current failure episode is already logged
}

// NetworkChanged starts a new revision after the platform reports a network
// change. Resolver identities may not tell two networks apart (same interface,
// DHCP address and DNS server), so remembered answers are dropped and an
// in-flight lookup from before the change is rejected.
func (c *Cache) NetworkChanged() {
	c.mu.Lock()
	c.revision++
	c.lastGood = nil
	c.mu.Unlock()
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
	if err != nil {
		// Only a failed query in the same revision may use the last answer: not
		// a negative answer, a replaced network or a caller that gave up.
		if queryFailed && current == revision && parent.Err() == nil {
			revision = c.recapture(parent, source, revision)
			if stale, ok := c.staleAnswer(host, revision, err); ok {
				return stale, nil
			}
		}
		return nil, fmt.Errorf("resolve endpoint %s: %w", host, err)
	}
	c.remember(host, revision, addresses)
	return addresses, nil
}

// remember stores a successful answer unless the revision changed meanwhile.
// When the store is full, the oldest answer gives way.
func (c *Cache) remember(host string, revision uint64, addresses []net.IPAddr) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.revision != revision {
		return
	}
	if c.lastGood == nil {
		c.lastGood = make(map[string]staleAnswer)
	}
	if _, known := c.lastGood[host]; !known && len(c.lastGood) >= staleAnswerEntries {
		oldest := ""
		for name, answer := range c.lastGood {
			if oldest == "" || answer.at.Before(c.lastGood[oldest].at) {
				oldest = name
			}
		}
		delete(c.lastGood, oldest)
	}
	c.lastGood[host] = staleAnswer{addresses: append([]net.IPAddr(nil), addresses...), revision: revision, at: now()}
}

// recapture reads the physical network again after a failed query: a native
// resolver may fail because the network was replaced before the cache's
// revision changed. A new identity starts a new revision, so the answer from
// the old network is not used. It returns the revision to check.
func (c *Cache) recapture(ctx context.Context, source ResolverSource, revision uint64) uint64 {
	if source == nil {
		return revision
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	identity, resolver, err := source(ctx)
	if err != nil {
		return 0 // no current network: nothing may be served
	}
	c.SetResolver(identity, resolver)
	return revision
}

// staleAnswer returns the last answer for host if it belongs to the current
// revision and is recent enough. The revision is checked under the same lock
// as the decision. The first use in a failure episode is logged once; a
// successful lookup ends it.
func (c *Cache) staleAnswer(host string, revision uint64, cause error) ([]net.IPAddr, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	answer, ok := c.lastGood[host]
	age := now().Sub(answer.at)
	if !ok || revision == 0 || answer.revision != revision || c.revision != revision || age > StaleAnswerLimit {
		return nil, false
	}
	if !answer.warned {
		answer.warned = true
		c.lastGood[host] = answer
		log.Warn(Category, "underlying DNS failed; using the last known endpoint address", map[string]any{
			"host": host, "answer_age_ms": age.Milliseconds(), "cause": cause.Error(),
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
