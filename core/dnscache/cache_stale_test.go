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
	addresses, err := cache.ResolveServerAddresses(context.Background(), "endpoint.example")
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
	if _, err := cache.ResolveServerAddresses(context.Background(), "endpoint.example"); err == nil {
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
	if _, err := cache.ResolveServerAddresses(context.Background(), "endpoint.example"); err == nil {
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
	if _, err := cache.ResolveServerAddresses(context.Background(), "endpoint.example"); err == nil {
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
	if _, err := cache.ResolveServerAddresses(context.Background(), "endpoint.example"); err == nil {
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
		if _, err := cache.ResolveServerAddresses(context.Background(), fmt.Sprintf("host%d.example", i)); err != nil {
			t.Fatal(err)
		}
	}
	if len(cache.lastGood) != staleAnswerEntries {
		t.Fatalf("remembered %d names, want at most %d", len(cache.lastGood), staleAnswerEntries)
	}
	fail = true
	if _, err := cache.ResolveServerAddresses(context.Background(), "host0.example"); err == nil {
		t.Fatal("the oldest name must have been evicted")
	}
	if _, err := cache.ResolveServerAddresses(context.Background(), fmt.Sprintf("host%d.example", staleAnswerEntries)); err != nil {
		t.Fatalf("the newest name must still be remembered: %v", err)
	}
}

func TestLastKnownAnswerExpires(t *testing.T) {
	clock := useClock(t)
	cache := New()
	cache.SetResolver("network-a", scriptedResolver([]string{"192.0.2.1"}, nil))
	addressesOf(t, cache)
	*clock = clock.Add(StaleAnswerLimit + time.Minute)
	if _, err := cache.ResolveServerAddresses(context.Background(), "endpoint.example"); err == nil {
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
		if _, err := cache.ResolveServerAddresses(context.Background(), "endpoint.example"); err == nil {
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

// fakeTimer replaces the client response timer; closing the returned channel
// fires it for every waiting caller.
func fakeTimer(t *testing.T) chan time.Time {
	t.Helper()
	fire := make(chan time.Time)
	old := staleTimer
	staleTimer = func() (<-chan time.Time, func() bool) { return fire, func() bool { return true } }
	t.Cleanup(func() { staleTimer = old })
	return fire
}

// blockingResolver answers the first query; every later query reports on
// started and then waits for next (or the query's context, if honorContext).
type blockingResolver struct {
	calls        atomic.Int32
	started      chan struct{}
	next         chan func() ([]net.IPAddr, error)
	honorContext bool
}

func newBlockingResolver(honorContext bool) *blockingResolver {
	return &blockingResolver{started: make(chan struct{}, 8), next: make(chan func() ([]net.IPAddr, error), 8), honorContext: honorContext}
}

func (b *blockingResolver) resolve(ctx context.Context, _ string) ([]net.IPAddr, error) {
	if b.calls.Add(1) == 1 {
		return []net.IPAddr{{IP: net.ParseIP("192.0.2.1")}}, nil
	}
	b.started <- struct{}{}
	done := ctx.Done()
	if !b.honorContext {
		done = nil
	}
	select {
	case answer := <-b.next:
		return answer()
	case <-done:
		return nil, ctx.Err()
	}
}

type lookupResult struct {
	addresses string
	err       error
}

func resolveAsync(ctx context.Context, cache *Cache) chan lookupResult {
	result := make(chan lookupResult, 1)
	go func() {
		addresses, err := cache.ResolveServerAddresses(ctx, "endpoint.example")
		text := make([]string, 0, len(addresses))
		for _, address := range addresses {
			text = append(text, address.IP.String())
		}
		result <- lookupResult{strings.Join(text, " "), err}
	}()
	return result
}

func TestClientResponseTimerIsOneAndAHalfSeconds(t *testing.T) {
	if staleAnswerWait != 1500*time.Millisecond {
		t.Fatalf("client response timer is %s, want 1.5s", staleAnswerWait)
	}
}

// A hanging DNS gives the remembered answer when the client response timer
// fires, well inside the caller's own 5 s deadline, and not before.
func TestHangingDNSServesRememberedAnswerAtClientTimer(t *testing.T) {
	useClock(t)
	fire := fakeTimer(t)
	resolver := newBlockingResolver(true)
	cache := New()
	t.Cleanup(cache.Close)
	cache.SetResolver("network-a", resolver.resolve)
	addressesOf(t, cache)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := resolveAsync(ctx, cache)
	<-resolver.started
	select {
	case got := <-result:
		t.Fatalf("answered before the timer: %+v", got)
	default:
	}
	close(fire)
	if got := <-result; got.err != nil || got.addresses != "192.0.2.1" {
		t.Fatalf("want the remembered answer, got %+v", got)
	}
}

// The query keeps running after the caller got the remembered answer; its
// later answer replaces the remembered one, and a later negative answer
// forgets it.
func TestBackgroundAnswersUpdateRememberedAnswer(t *testing.T) {
	useClock(t)
	fire := fakeTimer(t)
	resolver := newBlockingResolver(true)
	cache := New()
	t.Cleanup(cache.Close)
	cache.SetResolver("network-a", resolver.resolve)
	addressesOf(t, cache)
	close(fire) // every wait ends at once from here on

	background := func(answer func() ([]net.IPAddr, error)) lookupResult {
		result := resolveAsync(context.Background(), cache)
		<-resolver.started
		cache.mu.RLock()
		r := cache.refreshing["endpoint.example"]
		cache.mu.RUnlock()
		got := <-result
		resolver.next <- answer
		<-r.done
		return got
	}
	if got := background(func() ([]net.IPAddr, error) { return []net.IPAddr{{IP: net.ParseIP("192.0.2.2")}}, nil }); got.addresses != "192.0.2.1" {
		t.Fatalf("slow DNS must give the remembered answer: %+v", got)
	}
	if got := background(func() ([]net.IPAddr, error) {
		return nil, &net.DNSError{Err: "no such host", Name: "endpoint.example", IsNotFound: true}
	}); got.addresses != "192.0.2.2" {
		t.Fatalf("the background answer must have replaced the remembered one: %+v", got)
	}
	if cache.hasStaleAnswer("endpoint.example", cache.revision) {
		t.Fatal("a negative answer must forget the remembered answer")
	}
}

// A network change while the caller waits drops the remembered answer.
func TestNetworkChangeWhileWaitingDropsRememberedAnswer(t *testing.T) {
	useClock(t)
	fakeTimer(t)
	resolver := newBlockingResolver(true)
	cache := New()
	t.Cleanup(cache.Close)
	cache.SetResolver("network-a", resolver.resolve)
	addressesOf(t, cache)
	result := resolveAsync(context.Background(), cache)
	<-resolver.started
	cache.NetworkChanged()
	if got := <-result; got.err == nil {
		t.Fatalf("an answer from before the network change must not be used: %+v", got)
	}
}

// Two networks may report the same interface and DNS identity. The resolver
// from the new capture must supply the answer even while the old one is stuck.
func TestLiveEndpointLookupRecapturesNetworkAfterChange(t *testing.T) {
	for _, stop := range []string{"network change", "caller canceled", "session closed"} {
		t.Run(stop, func(t *testing.T) {
			cache := New()
			defer cache.Close()
			old := newBlockingResolver(false)
			var deadlines []time.Time
			cache.SetResolverSource(func(ctx context.Context) (string, Resolver, error) {
				deadline, _ := ctx.Deadline()
				deadlines = append(deadlines, deadline)
				if len(deadlines) <= 2 {
					return "network-a", old.resolve, nil
				}
				return "network-a", scriptedResolver([]string{"192.0.2.2"}), nil
			})
			if got := addressesOf(t, cache); got != "192.0.2.1" {
				t.Fatalf("initial endpoint = %s", got)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := resolveAsync(ctx, cache)
			<-old.started
			cache.mu.RLock()
			pending := cache.refreshing["endpoint.example"]
			cache.mu.RUnlock()
			defer func() {
				old.next <- func() ([]net.IPAddr, error) {
					return []net.IPAddr{{IP: net.ParseIP("192.0.2.1")}}, nil
				}
				<-pending.done
			}()
			switch stop {
			case "caller canceled":
				cancel()
			case "session closed":
				cache.Close()
			}
			cache.NetworkChanged()
			got := <-result
			if stop != "network change" {
				if got.err == nil || got.addresses != "" || len(deadlines) != 2 {
					t.Fatalf("%s: got %+v after %d captures", stop, got, len(deadlines))
				}
				return
			}
			if got.err != nil || got.addresses != "192.0.2.2" {
				t.Fatalf("live endpoint lookup got %q, %v; want the new network's answer", got.addresses, got.err)
			}
			if len(deadlines) != 3 || deadlines[1].IsZero() || !deadlines[2].Equal(deadlines[1]) {
				t.Fatalf("fresh capture must share the original lookup deadline: %v", deadlines)
			}
		})
	}
}

func TestEndpointLookupStopsAfterSecondNetworkChange(t *testing.T) {
	cache := New()
	defer cache.Close()
	captures := 0
	cache.SetResolverSource(func(context.Context) (string, Resolver, error) {
		captures++
		if captures > 2 {
			return "", nil, errors.New("unexpected third capture")
		}
		return "physical-link", func(context.Context, string) ([]net.IPAddr, error) {
			cache.NetworkChanged()
			return []net.IPAddr{{IP: net.ParseIP("192.0.2.1")}}, nil
		}, nil
	})
	addresses, err := cache.ResolveServerAddresses(context.Background(), "endpoint.example")
	if !errors.Is(err, errNetworkChanged) || len(addresses) != 0 || captures != 2 {
		t.Fatalf("got %v, %v after %d captures", addresses, err, captures)
	}
}

func TestEndpointLookupDiscardsInvalidatedCapture(t *testing.T) {
	cache := New()
	defer cache.Close()
	captures := 0
	cache.SetResolverSource(func(context.Context) (string, Resolver, error) {
		captures++
		if captures == 1 {
			cache.NetworkChanged()
			return "same-interface", func(context.Context, string) ([]net.IPAddr, error) {
				t.Fatal("used resolver captured before network change")
				return nil, nil
			}, nil
		}
		return "same-interface", scriptedResolver([]string{"192.0.2.2"}), nil
	})
	if got := addressesOf(t, cache); got != "192.0.2.2" || captures != 2 {
		t.Fatalf("endpoint = %s, captures = %d", got, captures)
	}
}

// Stopping the network session ends the background query and the wait.
func TestCloseEndsBackgroundQuery(t *testing.T) {
	useClock(t)
	fakeTimer(t)
	resolver := newBlockingResolver(true)
	cache := New()
	cache.SetResolver("network-a", resolver.resolve)
	addressesOf(t, cache)
	result := resolveAsync(context.Background(), cache)
	<-resolver.started
	cache.Close()
	if got := <-result; got.err == nil {
		t.Fatalf("a stopped session must not wait for its resolver: %+v", got)
	}
}

// A resolver that ignores its context does not hold the caller after the
// background query is cancelled.
func TestStuckResolverDoesNotHoldCaller(t *testing.T) {
	useClock(t)
	fakeTimer(t)
	resolver := newBlockingResolver(false)
	cache := New()
	t.Cleanup(func() {
		resolver.next <- func() ([]net.IPAddr, error) { return nil, context.Canceled }
		cache.Close()
	})
	cache.SetResolver("network-a", resolver.resolve)
	addressesOf(t, cache)
	result := resolveAsync(context.Background(), cache)
	<-resolver.started
	cache.NetworkChanged()
	if got := <-result; got.err == nil {
		t.Fatalf("want an error once the stuck query is cancelled: %+v", got)
	}
}

// Concurrent callers for one host share a single background query.
func TestConcurrentCallersShareOneBackgroundQuery(t *testing.T) {
	useClock(t)
	fire := fakeTimer(t)
	resolver := newBlockingResolver(true)
	cache := New()
	t.Cleanup(cache.Close)
	cache.SetResolver("network-a", resolver.resolve)
	addressesOf(t, cache)
	results := make([]chan lookupResult, 0, 3)
	results = append(results, resolveAsync(context.Background(), cache))
	<-resolver.started
	for range 2 {
		results = append(results, resolveAsync(context.Background(), cache))
	}
	close(fire)
	for _, result := range results {
		if got := <-result; got.err != nil || got.addresses != "192.0.2.1" {
			t.Fatalf("want the remembered answer, got %+v", got)
		}
	}
	if n := resolver.calls.Load(); n != 2 {
		t.Fatalf("resolver queried %d times, want 2 (one answer, one shared background query)", n)
	}
}

// A background query is registered and its answer returned only for the
// current revision.
func TestBackgroundQueryIsBoundToCurrentRevision(t *testing.T) {
	cache := New()
	t.Cleanup(cache.Close)
	cache.SetResolver("network-a", scriptedResolver([]string{"192.0.2.1"}))
	cache.mu.RLock()
	old := cache.revision
	cache.mu.RUnlock()
	cache.NetworkChanged()
	if r := cache.startRefresh(cache.resolver, "network-a", "endpoint.example", old); r != nil {
		t.Fatal("a query of a replaced revision must not be registered")
	}
	if _, ok := cache.ofRevision([]net.IPAddr{{IP: net.ParseIP("192.0.2.1")}}, old); ok {
		t.Fatal("an answer of a replaced revision must not be returned")
	}
}

// The platform may replace the network without telling the cache (Android
// advances its own revision). When the timer fires, the network is read again
// and a remembered answer of the old network is not used.
func TestTimerConfirmsNetworkBeforeServing(t *testing.T) {
	useClock(t)
	fire := fakeTimer(t)
	resolver := newBlockingResolver(true)
	cache := New()
	t.Cleanup(cache.Close)
	var captures atomic.Int32
	cache.SetResolverSource(func(context.Context) (string, Resolver, error) {
		if captures.Add(1) >= 3 { // the capture when the timer fires
			return "network-b", scriptedResolver(nil), nil
		}
		return "network-a", resolver.resolve, nil
	})
	addressesOf(t, cache)
	result := resolveAsync(context.Background(), cache)
	<-resolver.started
	close(fire)
	if got := <-result; got.err == nil {
		t.Fatalf("an answer of the replaced network must not be used: %+v", got)
	}
}

// Changing the bytes of a returned address does not change what is remembered.
func TestRememberedAnswerIsIsolatedFromCallers(t *testing.T) {
	useClock(t)
	cache := New()
	t.Cleanup(cache.Close)
	cache.SetResolver("network-a", scriptedResolver([]string{"192.0.2.1"}, nil, nil))
	fresh, err := cache.ResolveServerAddresses(context.Background(), "endpoint.example")
	if err != nil {
		t.Fatal(err)
	}
	fresh[0].IP[len(fresh[0].IP)-1] = 99
	stale, err := cache.ResolveServerAddresses(context.Background(), "endpoint.example")
	if err != nil || stale[0].IP.String() != "192.0.2.1" {
		t.Fatalf("remembered answer changed through a returned one: %v %v", stale, err)
	}
	stale[0].IP[len(stale[0].IP)-1] = 98
	if got := addressesOf(t, cache); got != "192.0.2.1" {
		t.Fatalf("remembered answer changed through a served one: %s", got)
	}
}
