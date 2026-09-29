package dnscache

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

type timeoutError struct{}

func (timeoutError) Error() string   { return "lookup timed out" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestSetAndResolveCacheHit(t *testing.T) {
	Clear()
	if !SetIPv4("Example.COM", "203.0.113.7", "test", time.Minute) {
		t.Fatal("SetIPv4 returned false")
	}

	ip, err := ResolveIPv4(context.Background(), "example.com", time.Nanosecond, "test")
	if err != nil {
		t.Fatalf("ResolveIPv4 returned error: %v", err)
	}
	if got := ip.String(); got != "203.0.113.7" {
		t.Fatalf("ip=%s, want 203.0.113.7", got)
	}

	cached, ok := LookupIPv4("EXAMPLE.com", "test-lookup")
	if !ok {
		t.Fatal("LookupIPv4 returned ok=false, want true")
	}
	if got := cached.String(); got != "203.0.113.7" {
		t.Fatalf("cached ip=%s, want 203.0.113.7", got)
	}
}

func TestNormalizeHost(t *testing.T) {
	if got := NormalizeHost(" Example.COM. "); got != "example.com" {
		t.Fatalf("NormalizeHost()=%q", got)
	}
	if got := NormalizeHost("[Example.COM]"); got != "example.com" {
		t.Fatalf("NormalizeHost()=%q", got)
	}
}

func TestResolvePreflightIPv4PinsResult(t *testing.T) {
	Clear()
	started := time.Now()
	ip, err := ResolvePreflightIPv4(
		context.Background(),
		"203.0.113.7",
		time.Nanosecond,
		"test-preflight",
	)
	if err != nil {
		t.Fatalf("ResolvePreflightIPv4 returned error: %v", err)
	}
	if got := ip.String(); got != "203.0.113.7" {
		t.Fatalf("ip=%s, want 203.0.113.7", got)
	}

	mu.RLock()
	cached, ok := entries["203.0.113.7"]
	mu.RUnlock()
	if !ok {
		t.Fatal("preflight result was not cached")
	}
	if cached.expiresAt.Before(started.Add(PreflightCacheTTL - time.Second)) {
		t.Fatalf("preflight cache expires too early: %s", cached.expiresAt)
	}
}

func TestResolvePreflightIPv4RetriesOneTimeout(t *testing.T) {
	Clear()
	calls := 0
	ip, err := resolvePreflightIPv4(
		context.Background(), "vpn.example", time.Second, "test-retry",
		func(context.Context, string, time.Duration, string) (net.IP, error) {
			calls++
			if calls == 1 {
				return nil, timeoutError{}
			}
			return net.ParseIP("203.0.113.8").To4(), nil
		},
	)
	if err != nil {
		t.Fatalf("resolvePreflightIPv4 returned error: %v", err)
	}
	if calls != 2 || ip.String() != "203.0.113.8" {
		t.Fatalf("calls=%d ip=%v, want 2 calls and 203.0.113.8", calls, ip)
	}
	if cached, ok := LookupIPv4("vpn.example", "test"); !ok || !cached.Equal(ip) {
		t.Fatalf("retried result was not cached: ip=%v ok=%v", cached, ok)
	}
}

func TestResolvePreflightIPv4DoesNotRetryPermanentFailure(t *testing.T) {
	Clear()
	calls := 0
	want := errors.New("no such host")
	_, err := resolvePreflightIPv4(
		context.Background(), "missing.example", time.Second, "test-permanent",
		func(context.Context, string, time.Duration, string) (net.IP, error) {
			calls++
			return nil, want
		},
	)
	if !errors.Is(err, want) || calls != 1 {
		t.Fatalf("err=%v calls=%d, want permanent error after one call", err, calls)
	}
}
