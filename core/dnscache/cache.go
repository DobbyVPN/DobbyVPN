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

// staleAnswerWait is the client response timer of RFC 8767: with a remembered
// answer at hand, a caller waits this long for the resolver before using it,
// plus at most staleRecaptureWait to confirm the network.
// A resolver that hangs would otherwise hold every new connection for the
// whole ServerResolveTimeout.
var staleAnswerWait = 1500 * time.Millisecond

// staleTimer starts the client response timer; a test seam.
var staleTimer = func() (<-chan time.Time, func() bool) {
	timer := time.NewTimer(staleAnswerWait)
	return timer.C, timer.Stop
}

// staleRecaptureWait bounds the network capture that must confirm the network
// before a remembered answer is served; the platform may change its network
// without telling the cache (Android advances its own revision).
var staleRecaptureWait = 250 * time.Millisecond

var errNetworkChanged = errors.New("underlying network changed during resolution")

// now is a test seam for answer ages.
var now = time.Now

// Resolver is scoped to the physical network. Its native resolver or captured
// DNS servers own caching and DNS64 answers; Dobby must not pin another CDN TTL.
// The only exception is serve-stale on resolver failure (RFC 8767): when a
// query fails with an error or timeout, or has not answered within
// staleAnswerWait, the last successful answer for the host from the same
// resolver revision may be used, at most StaleAnswerLimit old. The query then
// finishes in the background and its answer replaces the remembered one. Any
// resolver or network change starts a new revision, so an answer is never
// used on another network. With working DNS every lookup still goes to the
// resolver.
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
	// later query in the same revision fails or is slow.
	lastGood map[string]staleAnswer
	// refreshing holds at most one background query per host for the current
	// revision; a revision change and Close cancel them.
	refreshing map[string]*refresh
	// life bounds background queries to the network session that owns the
	// cache; Close ends it.
	life context.Context
	end  context.CancelFunc
}

// refresh is one query that may outlive its caller. Its fields are written
// before done is closed.
type refresh struct {
	revision  uint64
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	addresses []net.IPAddr
	err       error
	failed    bool
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
	c.cancelRefreshesLocked()
	c.mu.Unlock()
}

// cancelRefreshesLocked stops background queries of an old revision; their
// answers could not be remembered anyway.
func (c *Cache) cancelRefreshesLocked() {
	for _, r := range c.refreshing {
		r.cancel()
	}
	c.refreshing = nil
}

func New() *Cache {
	life, end := context.WithCancel(context.Background())
	return &Cache{revision: 1, life: life, end: end}
}

// Close cancels background queries when the network session that owns the
// cache stops. Lookups still work; new background queries end at once.
func (c *Cache) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.end()
	c.cancelRefreshesLocked()
}

func (c *Cache) SetResolverSource(source ResolverSource) {
	c.mu.Lock()
	c.source = source
	c.resolver = nil
	c.revision++
	c.cancelRefreshesLocked()
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
	c.cancelRefreshesLocked()
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
	if c.hasStaleAnswer(host, revision) {
		return c.resolveWithStaleAnswer(parent, source, resolve, identity, host, revision)
	}
	addresses, _, err := c.lookup(ctx, resolve, identity, host, revision)
	if err != nil {
		return nil, fmt.Errorf("resolve endpoint %s: %w", host, err)
	}
	return addresses, nil
}

// lookup runs one query. A successful answer is remembered and a negative one
// (no such host, empty answer) forgets the remembered answer; failed reports a
// query that failed in this revision.
func (c *Cache) lookup(ctx context.Context, resolve Resolver, identity, host string, revision uint64) ([]net.IPAddr, bool, error) {
	started := time.Now()
	addresses, err := resolve(ctx, host)
	var dnsErr *net.DNSError
	failed := (err != nil && (!errors.As(err, &dnsErr) || !dnsErr.IsNotFound)) || ctx.Err() != nil
	c.mu.RLock()
	current := c.revision
	c.mu.RUnlock()
	if current != revision {
		err = errors.Join(err, errNetworkChanged)
		failed = false
	}
	if ctx.Err() != nil {
		err = errors.Join(err, ctx.Err())
	}
	if err == nil && len(addresses) == 0 {
		err = errors.New("underlying resolver returned no addresses")
	}
	log.Debugf(Category, "underlying lookup network=%s revision=%d host=%s addresses=%v elapsed=%s err=%v", identity, revision, host, addresses, time.Since(started), err)
	if err != nil {
		if !failed && current == revision {
			c.forget(host, revision)
		}
		return nil, failed, err
	}
	c.remember(host, revision, addresses)
	return addresses, false, nil
}

// hasStaleAnswer reports a remembered answer that could be served now.
func (c *Cache) hasStaleAnswer(host string, revision uint64) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	answer, ok := c.lastGood[host]
	return ok && answer.revision == revision && c.revision == revision && now().Sub(answer.at) <= StaleAnswerLimit
}

// resolveWithStaleAnswer waits at most staleAnswerWait for the resolver while a
// remembered answer is at hand. The query keeps running in the background,
// bounded by ServerResolveTimeout and the cache's life, and a later answer
// replaces the remembered one. Concurrent callers for one host share a query.
func (c *Cache) resolveWithStaleAnswer(parent context.Context, source ResolverSource, resolve Resolver, identity, host string, revision uint64) ([]net.IPAddr, error) {
	r := c.startRefresh(resolve, identity, host, revision)
	if r == nil {
		return nil, fmt.Errorf("resolve endpoint %s: %w", host, errNetworkChanged)
	}
	waitEnd := time.Now().Add(staleAnswerWait)
	timer, stop := staleTimer()
	defer stop()
	for {
		select {
		case <-r.done:
			if r.err == nil {
				if addresses, ok := c.ofRevision(r.addresses, revision); ok {
					return addresses, nil
				}
				return nil, fmt.Errorf("resolve endpoint %s: %w", host, errNetworkChanged)
			}
			if r.failed && c.life.Err() == nil {
				// The failure may come from a replaced network; read it again.
				current := c.confirmNetwork(parent, source, revision, waitEnd)
				if parent.Err() == nil {
					if stale, ok := c.staleAnswer(host, current, r.err); ok {
						return stale, nil
					}
				}
			}
			return nil, fmt.Errorf("resolve endpoint %s: %w", host, r.err)
		case <-timer:
			timer = nil
			slow := fmt.Errorf("underlying DNS has not answered within %s", staleAnswerWait)
			if parent.Err() == nil && c.life.Err() == nil {
				// The network may have changed while waiting; confirm it.
				current := c.confirmNetwork(parent, source, revision, waitEnd)
				if stale, ok := c.staleAnswer(host, current, slow); ok {
					return stale, nil
				}
			}
			// The network changed: only the query's own result may be used.
		case <-r.ctx.Done():
			// A resolver that ignores its context must not hold the caller.
			select {
			case <-r.done:
				continue
			default:
			}
			return nil, fmt.Errorf("resolve endpoint %s: %w", host, r.ctx.Err())
		case <-parent.Done():
			return nil, fmt.Errorf("resolve endpoint %s: %w", host, parent.Err())
		}
	}
}

// startRefresh registers the background query for host in revision, or joins
// the running one. It returns nil if the revision is no longer current. A
// query past its deadline is replaced, so a stuck resolver blocks nothing.
func (c *Cache) startRefresh(resolve Resolver, identity, host string, revision uint64) *refresh {
	c.mu.Lock()
	if c.revision != revision {
		c.mu.Unlock()
		return nil
	}
	if r := c.refreshing[host]; r != nil && r.revision == revision && r.ctx.Err() == nil {
		c.mu.Unlock()
		return r
	}
	ctx, cancel := context.WithTimeout(c.life, ServerResolveTimeout)
	r := &refresh{revision: revision, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	if c.refreshing == nil {
		c.refreshing = make(map[string]*refresh)
	}
	c.refreshing[host] = r
	c.mu.Unlock()
	go func() {
		defer cancel()
		r.addresses, r.failed, r.err = c.lookup(ctx, resolve, identity, host, revision)
		c.mu.Lock()
		if c.refreshing[host] == r {
			delete(c.refreshing, host)
		}
		c.mu.Unlock()
		close(r.done)
	}()
	return r
}

// ofRevision copies a background answer if its revision is still current.
func (c *Cache) ofRevision(addresses []net.IPAddr, revision uint64) ([]net.IPAddr, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.revision != revision {
		return nil, false
	}
	return cloneAddresses(addresses), true
}

// cloneAddresses copies the address structs and their IP bytes, so callers and
// the remembered answers never share memory.
func cloneAddresses(addresses []net.IPAddr) []net.IPAddr {
	clone := make([]net.IPAddr, len(addresses))
	for i, address := range addresses {
		clone[i] = net.IPAddr{IP: append(net.IP(nil), address.IP...), Zone: address.Zone}
	}
	return clone
}

// forget drops the remembered answer after a negative answer in revision.
func (c *Cache) forget(host string, revision uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if answer, ok := c.lastGood[host]; ok && answer.revision == revision && c.revision == revision {
		delete(c.lastGood, host)
	}
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
	c.lastGood[host] = staleAnswer{addresses: cloneAddresses(addresses), revision: revision, at: now()}
}

// confirmNetwork reads the network again within what is left of the wait, but
// never with less than staleRecaptureWait, and returns the revision to check.
func (c *Cache) confirmNetwork(parent context.Context, source ResolverSource, revision uint64, waitEnd time.Time) uint64 {
	deadline := time.Now().Add(staleRecaptureWait)
	if waitEnd.After(deadline) {
		deadline = waitEnd
	}
	ctx, cancel := context.WithDeadline(parent, deadline)
	defer cancel()
	return c.recapture(ctx, source, revision)
}

// recapture reads the physical network again after a failed query: a native
// resolver may fail because the network was replaced before the cache's
// revision changed. A new identity starts a new revision, so the answer from
// the old network is not used. It returns the revision to check.
func (c *Cache) recapture(ctx context.Context, source ResolverSource, revision uint64) uint64 {
	if source == nil {
		return revision
	}
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
	return cloneAddresses(answer.addresses), true
}

func NormalizeHost(host string) string {
	host = strings.TrimSpace(host)
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	if _, err := netip.ParseAddr(host); err == nil {
		return host
	}
	return strings.TrimSuffix(strings.ToLower(host), ".")
}
