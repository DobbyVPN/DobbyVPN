package runtime

import (
	"testing"
	"time"

	"core/dnscache"
)

// Start holds the runtime lock while it resolves bypass hosts. A reported
// network change must reach the cache without waiting for that lock.
func TestNetworkChangedDoesNotWaitForRuntimeLock(t *testing.T) {
	r := &runtime{}
	cache := dnscache.New()
	t.Cleanup(cache.Close)
	r.cache.Store(cache)
	r.mu.Lock()
	defer r.mu.Unlock()
	done := make(chan struct{})
	go func() {
		r.NetworkChanged()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("NetworkChanged waited for the runtime lock")
	}
}
