package dnscache

import (
	"context"
	"testing"
	"time"
)

func TestSetEntriesAndResolveCacheHit(t *testing.T) {
	Clear()
	stored := SetEntries("Example.COM=203.0.113.7\nbad-entry\n", "test", time.Minute)
	if stored != 1 {
		t.Fatalf("stored=%d, want 1", stored)
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
