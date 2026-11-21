package dnscache

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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
	text := make([]string, 0, len(addresses))
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

// Two networks can share a resolver identity (same interface, DHCP address
// and DNS server). A reported network change must still drop the answer.
func TestLastKnownAnswerSkipsSameIdentityAfterNetworkChange(t *testing.T) {
	useClock(t)
	cache := New()
	cache.SetResolver("network-a", scriptedResolver([]string{"192.0.2.1"}, nil))
	addressesOf(t, cache)
	cache.NetworkChanged()
	if _, err := cache.ResolveAddresses(context.Background(), "endpoint.example"); err == nil {
		t.Fatal("an answer from before a network change must not be used")
	}
}

// A network change between the failed query and the decision to serve the
// remembered answer is seen under the same lock as that decision.
func TestLastKnownAnswerSkipsChangeBeforeServing(t *testing.T) {
	useClock(t)
	cache := New()
	captures := 0
	resolver := scriptedResolver([]string{"192.0.2.1"}, nil)
	cache.SetResolverSource(func(context.Context) (string, Resolver, error) {
		captures++
		if captures == 3 { // the recapture after the failed query
			cache.NetworkChanged()
		}
		return "network-a", resolver, nil
	})
	addressesOf(t, cache)
	if _, err := cache.ResolveAddresses(context.Background(), "endpoint.example"); err == nil {
		t.Fatal("a network change before serving must reject the remembered answer")
	}
}

// The store keeps a bounded number of host names; the oldest gives way.
func TestLastKnownAnswersAreBounded(t *testing.T) {
	clock := useClock(t)
	cache := New()
	fail := false
	cache.SetResolver("network-a", func(context.Context, string) ([]net.IPAddr, error) {
		if fail {
			return nil, context.DeadlineExceeded
		}
		return []net.IPAddr{{IP: net.ParseIP("192.0.2.1")}}, nil
	})
	for i := range staleAnswerEntries + 1 {
		*clock = clock.Add(time.Second)
		if _, err := cache.ResolveAddresses(context.Background(), fmt.Sprintf("host%d.example", i)); err != nil {
			t.Fatal(err)
		}
	}
	if len(cache.lastGood) != staleAnswerEntries {
		t.Fatalf("remembered %d names, want at most %d", len(cache.lastGood), staleAnswerEntries)
	}
	fail = true
	if _, err := cache.ResolveAddresses(context.Background(), "host0.example"); err == nil {
		t.Fatal("the oldest name must have been evicted")
	}
	if _, err := cache.ResolveAddresses(context.Background(), fmt.Sprintf("host%d.example", staleAnswerEntries)); err != nil {
		t.Fatalf("the newest name must still be remembered: %v", err)
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

// hangingAfterFirst answers the first query and then hangs until the query's
// context ends, like a router DNS that stops answering.
func hangingAfterFirst(calls *atomic.Int32) Resolver {
	return func(ctx context.Context, _ string) ([]net.IPAddr, error) {
		if calls.Add(1) == 1 {
			return []net.IPAddr{{IP: net.ParseIP("192.0.2.1")}}, nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
}

func useWait(t *testing.T, wait time.Duration) {
	t.Helper()
	old := staleAnswerWait
	staleAnswerWait = wait
	t.Cleanup(func() { staleAnswerWait = old })
}

// A hanging DNS gives the remembered answer after the client response timer,
// well inside the caller's own deadline.
func TestHangingDNSServesRememberedAnswerWithinClientTimer(t *testing.T) {
	useClock(t)
	var calls atomic.Int32
	cache := New()
	cache.SetResolver("network-a", hangingAfterFirst(&calls))
	t.Cleanup(cache.NetworkChanged) // stop the background query
	addressesOf(t, cache)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := time.Now()
	addresses, err := cache.ResolveAddresses(ctx, "endpoint.example")
	elapsed := time.Since(started)
	if err != nil || len(addresses) != 1 || addresses[0].IP.String() != "192.0.2.1" {
		t.Fatalf("want the remembered answer, got %v %v", addresses, err)
	}
	if elapsed < staleAnswerWait-100*time.Millisecond || elapsed > staleAnswerWait+time.Second {
		t.Fatalf("remembered answer after %s, want about %s", elapsed, staleAnswerWait)
	}
}

// The query keeps running after the caller got the remembered answer; its
// later answer replaces the remembered one.
func TestBackgroundAnswerReplacesRememberedAnswer(t *testing.T) {
	useClock(t)
	useWait(t, 50*time.Millisecond)
	release := make(chan struct{})
	var calls atomic.Int32
	cache := New()
	cache.SetResolver("network-a", func(ctx context.Context, _ string) ([]net.IPAddr, error) {
		switch calls.Add(1) {
		case 1:
			return []net.IPAddr{{IP: net.ParseIP("192.0.2.1")}}, nil
		case 2:
			select {
			case <-release:
				return []net.IPAddr{{IP: net.ParseIP("192.0.2.2")}}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		default:
			return nil, context.DeadlineExceeded
		}
	})
	addressesOf(t, cache)
	if got := addressesOf(t, cache); got != "192.0.2.1" {
		t.Fatalf("slow DNS must give the remembered answer: %s", got)
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		cache.mu.RLock()
		pending := len(cache.refreshing)
		cache.mu.RUnlock()
		if pending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background query did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := addressesOf(t, cache); got != "192.0.2.2" {
		t.Fatalf("the background answer must replace the remembered one: %s", got)
	}
}

// A network change while the caller waits drops the remembered answer.
func TestNetworkChangeWhileWaitingDropsRememberedAnswer(t *testing.T) {
	useClock(t)
	useWait(t, time.Second)
	var calls atomic.Int32
	cache := New()
	cache.SetResolver("network-a", hangingAfterFirst(&calls))
	addressesOf(t, cache)
	go func() {
		time.Sleep(100 * time.Millisecond)
		cache.NetworkChanged()
	}()
	started := time.Now()
	if _, err := cache.ResolveAddresses(context.Background(), "endpoint.example"); err == nil {
		t.Fatal("an answer from before the network change must not be used")
	}
	if elapsed := time.Since(started); elapsed >= staleAnswerWait {
		t.Fatalf("the network change must end the wait, took %s", elapsed)
	}
}

// Concurrent callers for one host share a single background query.
func TestConcurrentCallersShareOneBackgroundQuery(t *testing.T) {
	useClock(t)
	useWait(t, 50*time.Millisecond)
	var calls atomic.Int32
	cache := New()
	cache.SetResolver("network-a", hangingAfterFirst(&calls))
	t.Cleanup(cache.NetworkChanged)
	addressesOf(t, cache)
	results := make(chan string, 3)
	for range 3 {
		go func() {
			addresses, err := cache.ResolveAddresses(context.Background(), "endpoint.example")
			if err != nil || len(addresses) == 0 {
				results <- fmt.Sprint(err)
				return
			}
			results <- addresses[0].IP.String()
		}()
	}
	for range 3 {
		if got := <-results; got != "192.0.2.1" {
			t.Fatalf("want the remembered answer, got %s", got)
		}
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("resolver queried %d times, want 2 (one answer, one shared background query)", n)
	}
}
