package dnscache

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"go_module/log"
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
	source    string
}

var (
	mu      sync.RWMutex
	entries = map[string]entry{}
)

func Clear() {
	mu.Lock()
	defer mu.Unlock()
	entries = map[string]entry{}
	log.Debugf(Category, "cleared")
}

func SetIPv4(host, ipString, source string, ttl time.Duration) bool {
	host = NormalizeHost(host)
	ip := net.ParseIP(strings.TrimSpace(ipString))
	if host == "" || ip == nil || ip.To4() == nil || ttl <= 0 {
		return false
	}

	mu.Lock()
	defer mu.Unlock()
	entries[host] = entry{
		ip:        append(net.IP(nil), ip.To4()...),
		expiresAt: time.Now().Add(ttl),
		source:    source,
	}
	log.Debugf(Category, "stored source=%s ttl=%s", source, ttl)
	return true
}

func ResolveIPv4(ctx context.Context, host string, timeout time.Duration, source string) (net.IP, error) {
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

	if ip, ok := LookupIPv4(host, source); ok {
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
			SetIPv4(host, ip4.String(), source, time.Minute)
			return ip4, nil
		}
	}
	log.Debugf(Category, "lookup returned no IPv4 source=%s elapsed=%s addresses=%d", source, elapsed, len(addrs))
	return nil, errors.New("DNS resolved only IPv6, IPv4 required")
}

// ResolvePreflightIPv4 pins a successful bootstrap lookup for the session.
func ResolvePreflightIPv4(ctx context.Context, host string, timeout time.Duration, source string) (net.IP, error) {
	return resolvePreflightIPv4(ctx, host, timeout, source, ResolveIPv4)
}

func resolvePreflightIPv4(
	ctx context.Context,
	host string,
	timeout time.Duration,
	source string,
	resolve func(context.Context, string, time.Duration, string) (net.IP, error),
) (net.IP, error) {
	var lastErr error
	for attempt := 1; attempt <= preflightAttempts; attempt++ {
		ip, err := resolve(ctx, host, timeout, source)
		if err == nil {
			if !SetIPv4(host, ip.String(), source, PreflightCacheTTL) {
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

func LookupIPv4(host, source string) (net.IP, bool) {
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

	if ip, ok := lookup(host); ok {
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

func lookup(host string) (net.IP, bool) {
	now := time.Now()
	mu.RLock()
	cached, ok := entries[host]
	mu.RUnlock()
	if !ok {
		return nil, false
	}
	if now.After(cached.expiresAt) {
		mu.Lock()
		if current, exists := entries[host]; exists && now.After(current.expiresAt) {
			delete(entries, host)
		}
		mu.Unlock()
		return nil, false
	}
	return append(net.IP(nil), cached.ip...), true
}
