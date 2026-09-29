package dnscache

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"core/log"
)

const (
	Category            = "DNSCache"
	PreflightCacheTTL   = 12 * time.Hour
	preflightAttempts   = 2
	preflightRetryDelay = 250 * time.Millisecond
	// ServerResolveTimeout bounds each mandatory bootstrap lookup attempt used
	// to establish the VPN server route. A fresh macOS daemon can need longer
	// than two seconds for its first resolver request after launchd restarts it.
	ServerResolveTimeout = 5 * time.Second
)

type entry struct {
	ip        net.IP
	expiresAt time.Time
}

// Cache belongs to one runtime start attempt. Its lifetime is the attempt's
// lifetime, so retrying or replacing a session cannot reuse an earlier
// bootstrap address.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]entry
}

func New() *Cache {
	return &Cache{entries: make(map[string]entry)}
}

func (c *Cache) SetIPv4(host, ipString, source string, ttl time.Duration) bool {
	host = NormalizeHost(host)
	ip := net.ParseIP(strings.TrimSpace(ipString))
	if c == nil || host == "" || ip == nil || ip.To4() == nil || ttl <= 0 {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]entry)
	}
	c.entries[host] = entry{
		ip:        append(net.IP(nil), ip.To4()...),
		expiresAt: time.Now().Add(ttl),
	}
	log.Debugf(Category, "stored source=%s ttl=%s", source, ttl)
	return true
}

func (c *Cache) ResolveIPv4(ctx context.Context, host string, timeout time.Duration, source string) (net.IP, error) {
	if c == nil {
		return nil, errors.New("DNS cache is required")
	}
	host = NormalizeHost(host)
	if host == "" {
		return nil, errors.New("empty host")
	}

	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			return ip4, nil
		}
		return nil, errors.New("IPv6 address not supported; routing requires IPv4")
	}

	if ip, ok := c.LookupIPv4(host, source); ok {
		return ip, nil
	}

	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	startedAt := time.Now()
	resolver := net.Resolver{}
	addrs, err := resolver.LookupIPAddr(ctx, host)
	elapsed := time.Since(startedAt).Truncate(time.Millisecond)
	if err != nil {
		log.Debugf(Category, "lookup failed source=%s elapsed=%s errorType=%T error=%v", source, elapsed, err, err)
		return nil, fmt.Errorf("DNS resolve failed: %w", err)
	}

	for _, addr := range addrs {
		if ip4 := addr.IP.To4(); ip4 != nil {
			log.Debugf(Category, "lookup resolved source=%s elapsed=%s", source, elapsed)
			c.SetIPv4(host, ip4.String(), source, time.Minute)
			return ip4, nil
		}
	}
	log.Debugf(Category, "lookup returned no IPv4 source=%s elapsed=%s addresses=%d", source, elapsed, len(addrs))
	return nil, errors.New("DNS resolved only IPv6, IPv4 required")
}

// ResolvePreflightIPv4 pins a successful bootstrap lookup for this attempt.
func (c *Cache) ResolvePreflightIPv4(ctx context.Context, host string, timeout time.Duration, source string) (net.IP, error) {
	if c == nil {
		return nil, errors.New("DNS cache is required")
	}
	return resolvePreflightIPv4(ctx, host, timeout, source, c.ResolveIPv4, c.SetIPv4)
}

func resolvePreflightIPv4(
	ctx context.Context,
	host string,
	timeout time.Duration,
	source string,
	resolve func(context.Context, string, time.Duration, string) (net.IP, error),
	set func(string, string, string, time.Duration) bool,
) (net.IP, error) {
	var lastErr error
	for attempt := 1; attempt <= preflightAttempts; attempt++ {
		ip, err := resolve(ctx, host, timeout, source)
		if err == nil {
			if !set(host, ip.String(), source, PreflightCacheTTL) {
				return nil, errors.New("failed to cache preflight IPv4")
			}
			return ip, nil
		}
		lastErr = err
		var networkError net.Error
		if attempt == preflightAttempts || !errors.As(err, &networkError) || !networkError.Timeout() {
			return nil, err
		}
		log.Debugf(Category, "preflight lookup retry source=%s attempt=%d", source, attempt+1)
		timer := time.NewTimer(preflightRetryDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, lastErr
}

func (c *Cache) LookupIPv4(host, source string) (net.IP, bool) {
	if c == nil {
		return nil, false
	}
	host = NormalizeHost(host)
	if host == "" {
		return nil, false
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			return ip4, true
		}
		return nil, false
	}

	if ip, ok := c.lookup(host); ok {
		log.Debugf(Category, "cache hit source=%s", source)
		return ip, true
	}
	return nil, false
}

func NormalizeHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimPrefix(strings.TrimSuffix(host, "."), "[")
	host = strings.TrimSuffix(host, "]")
	return host
}

func (c *Cache) lookup(host string) (net.IP, bool) {
	now := time.Now()
	c.mu.RLock()
	cached, ok := c.entries[host]
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if now.After(cached.expiresAt) {
		c.mu.Lock()
		if current, exists := c.entries[host]; exists && now.After(current.expiresAt) {
			delete(c.entries, host)
		}
		c.mu.Unlock()
		return nil, false
	}
	return append(net.IP(nil), cached.ip...), true
}
