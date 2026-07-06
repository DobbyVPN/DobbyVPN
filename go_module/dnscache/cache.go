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
	Category           = "DNSCache"
	PreflightCacheTTL  = 12 * time.Hour
	FastResolveTimeout = 750 * time.Millisecond
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
	if host == "" || ip == nil || ip.To4() == nil {
		return false
	}
	if ttl <= 0 {
		ttl = time.Minute
	}

	mu.Lock()
	defer mu.Unlock()
	entries[host] = entry{
		ip:        append(net.IP(nil), ip.To4()...),
		expiresAt: time.Now().Add(ttl),
		source:    source,
	}
	log.Debugf(Category, "stored host=%s ip=%s source=%s ttl=%s", host, ip.To4().String(), source, ttl)
	return true
}

func SetEntries(lines, source string, ttl time.Duration) int {
	count := 0
	for _, line := range strings.Split(lines, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		host, ip, ok := strings.Cut(line, "=")
		if !ok {
			log.Debugf(Category, "skip malformed preflight entry: %q", line)
			continue
		}
		if SetIPv4(host, ip, source, ttl) {
			count++
		}
	}
	log.Debugf(Category, "preflight stored entries=%d source=%s", count, source)
	return count
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
		log.Debugf(Category, "lookup failed host=%s source=%s elapsed=%s err=%v", host, source, elapsed, err)
		return nil, fmt.Errorf("DNS resolve failed for %q: %w", host, err)
	}

	for _, addr := range addrs {
		if ip4 := addr.IP.To4(); ip4 != nil {
			log.Debugf(Category, "lookup resolved host=%s ip=%s source=%s elapsed=%s", host, ip4.String(), source, elapsed)
			SetIPv4(host, ip4.String(), source, time.Minute)
			return ip4, nil
		}
	}
	log.Debugf(Category, "lookup returned no IPv4 host=%s source=%s elapsed=%s addresses=%d", host, source, elapsed, len(addrs))
	return nil, errors.New("DNS resolved only IPv6, IPv4 required")
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
		log.Debugf(Category, "cache hit host=%s ip=%s source=%s", host, ip.String(), source)
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
