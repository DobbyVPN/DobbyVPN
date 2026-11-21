package dnscache

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"core/log"
)

// scriptedResolver answers each lookup with the next entry; nil addresses
// mean the query fails like an unanswered DNS server.
func scriptedResolver(answers ...[]string) Resolver {
	return func(context.Context, string) ([]net.IPAddr, error) {
		if len(answers) == 0 {
			return nil, errors.New("no more answers")
		}
		next := answers[0]
		answers = answers[1:]
		if next == nil {
			return nil, context.DeadlineExceeded
		}
		var result []net.IPAddr
		for _, address := range next {
			result = append(result, net.IPAddr{IP: net.ParseIP(address)})
		}
		return result, nil
	}
}

func useClock(t *testing.T) *time.Time {
	t.Helper()
	clock := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	old := now
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = old })
	return &clock
}

func addressesOf(t *testing.T, cache *Cache) string {
	t.Helper()
	addresses, err := cache.ResolveAddresses(context.Background(), "endpoint.example")
	if err != nil {
		t.Fatal(err)
	}
	var text []string
	for _, address := range addresses {
		text = append(text, address.IP.String())
	}
	return strings.Join(text, " ")
}

func TestWorkingDNSAlwaysAnswersFresh(t *testing.T) {
	useClock(t)
	cache := New()
	cache.SetResolver("network-a", scriptedResolver([]string{"192.0.2.1"}, []string{"192.0.2.2"}))
	if got := addressesOf(t, cache); got != "192.0.2.1" {
		t.Fatalf("first answer %s", got)
	}
	if got := addressesOf(t, cache); got != "192.0.2.2" {
		t.Fatalf("working DNS must not be served from memory: %s", got)
	}
}

func TestFailedDNSUsesLastKnownAnswer(t *testing.T) {
	clock := useClock(t)
	cache := New()
	cache.SetResolver("network-a", scriptedResolver([]string{"192.0.2.1"}, nil))
	addressesOf(t, cache)
	*clock = clock.Add(time.Hour)
	if got := addressesOf(t, cache); got != "192.0.2.1" {
		t.Fatalf("failed DNS must use the last known answer: %s", got)
	}
}

func TestLastKnownAnswerStaysOnItsNetwork(t *testing.T) {
	useClock(t)
	cache := New()
	cache.SetResolver("network-a", scriptedResolver([]string{"192.0.2.1"}))
	addressesOf(t, cache)
	cache.SetResolver("network-b", scriptedResolver(nil))
	if _, err := cache.ResolveAddresses(context.Background(), "endpoint.example"); err == nil {
		t.Fatal("another network must not use the first network's answer")
	}
}

func TestLastKnownAnswerSkipsReplacedNetworkDuringQuery(t *testing.T) {
	useClock(t)
	cache := New()
	captures := []string{"network-a", "network-a", "network-b"}
	resolvers := []Resolver{scriptedResolver([]string{"192.0.2.1"}), scriptedResolver(nil)}
	cache.SetResolverSource(func(context.Context) (string, Resolver, error) {
		identity := captures[0]
		captures = captures[1:]
		resolver := resolvers[0]
		if len(resolvers) > 1 {
			resolvers = resolvers[1:]
		}
		return identity, resolver, nil
	})
	addressesOf(t, cache)
	if _, err := cache.ResolveAddresses(context.Background(), "endpoint.example"); err == nil {
		t.Fatal("a network replaced during the failed query must not use the old answer")
	}
}

func TestLastKnownAnswerExpires(t *testing.T) {
	clock := useClock(t)
	cache := New()
	cache.SetResolver("network-a", scriptedResolver([]string{"192.0.2.1"}, nil))
	addressesOf(t, cache)
	*clock = clock.Add(StaleAnswerLimit + time.Minute)
	if _, err := cache.ResolveAddresses(context.Background(), "endpoint.example"); err == nil {
		t.Fatal("an answer older than the limit must not be used")
	}
}

func TestNegativeAnswerIsNotReplaced(t *testing.T) {
	useClock(t)
	cache := New()
	calls := 0
	cache.SetResolver("network-a", func(context.Context, string) ([]net.IPAddr, error) {
		calls++
		switch calls {
		case 1:
			return []net.IPAddr{{IP: net.ParseIP("192.0.2.1")}}, nil
		case 2:
			return nil, &net.DNSError{Err: "no such host", Name: "endpoint.example", IsNotFound: true}
		default:
			return nil, nil // an empty answer
		}
	})
	addressesOf(t, cache)
	for range 2 {
		if _, err := cache.ResolveAddresses(context.Background(), "endpoint.example"); err == nil {
			t.Fatal("a negative or empty answer must not be replaced by the last known one")
		}
	}
}

func TestLastKnownAnswerWarnsOncePerEpisode(t *testing.T) {
	useClock(t)
	path := filepath.Join(t.TempDir(), "backend.jsonl")
	if err := log.SetPath(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	// Earlier records may be carried into the new file; count after a marker.
	log.Info(Category, "serve-stale episode test start", nil)
	cache := New()
	cache.SetResolver("network-a", scriptedResolver([]string{"192.0.2.1"}, nil, nil, nil, []string{"192.0.2.1"}, nil))
	for range 6 {
		addressesOf(t, cache)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, after, found := strings.Cut(string(raw), "serve-stale episode test start")
	if !found {
		t.Fatalf("marker missing:\n%s", raw)
	}
	raw = []byte(after)
	warnings := strings.Count(after, "using the last known endpoint address")
	if warnings != 2 {
		t.Fatalf("want one warning per failure episode (2 episodes), got %d:\n%s", warnings, raw)
	}
	if !strings.Contains(string(raw), "endpoint.example") || !strings.Contains(string(raw), "answer_age_ms") {
		t.Fatalf("warning must name the host and the answer age:\n%s", raw)
	}
}
