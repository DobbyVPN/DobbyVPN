package tunnel

import (
	"context"
	"errors"
	"sync"
	"testing"

	"core/dnscache"
	"core/tunnel/platform_engine"
)

func TestOwnedEngineStopIsIdempotent(t *testing.T) {
	stops := 0
	e := &Engine{stopPlatform: func(context.Context) error { stops++; return nil }}
	engineMu.Lock()
	previous := activeEngine
	activeEngine = e
	engineMu.Unlock()
	defer func() {
		engineMu.Lock()
		activeEngine = previous
		engineMu.Unlock()
	}()

	e.Stop(context.Background())
	e.Stop(context.Background())
	if stops != 1 {
		t.Fatalf("platform stops = %d, want 1", stops)
	}
}

func TestOwnedEngineStopReturnsTheSameCleanupError(t *testing.T) {
	want := errors.New("cleanup failed")
	e := &Engine{stopPlatform: func(context.Context) error { return want }}
	engineMu.Lock()
	previous := activeEngine
	activeEngine = e
	engineMu.Unlock()
	defer func() {
		engineMu.Lock()
		activeEngine = previous
		engineMu.Unlock()
	}()

	if err := e.Stop(context.Background()); !errors.Is(err, want) {
		t.Fatalf("first Stop error=%v, want %v", err, want)
	}
	if err := e.Stop(context.Background()); !errors.Is(err, want) {
		t.Fatalf("second Stop error=%v, want %v", err, want)
	}
}

func TestStaleEngineCannotStopCurrentOwner(t *testing.T) {
	oldStops, currentStops := 0, 0
	old := &Engine{stopPlatform: func(context.Context) error { oldStops++; return nil }}
	current := &Engine{stopPlatform: func(context.Context) error { currentStops++; return nil }}
	engineMu.Lock()
	previous := activeEngine
	activeEngine = current
	engineMu.Unlock()
	defer func() {
		engineMu.Lock()
		activeEngine = previous
		engineMu.Unlock()
	}()

	old.Stop(context.Background())
	if oldStops != 0 || currentStops != 0 {
		t.Fatalf("stale stop affected platform: old=%d current=%d", oldStops, currentStops)
	}
}

func TestSecondOwnedStartReturnsBusyBeforeTouchingPlatform(t *testing.T) {
	engineMu.Lock()
	previous := activeEngine
	activeEngine = &Engine{}
	engineMu.Unlock()
	defer func() {
		engineMu.Lock()
		activeEngine = previous
		engineMu.Unlock()
	}()

	_, err := StartOwnedEngine(platform_engine.EngineConfig{}, dnscache.New(), nil)
	if !errors.Is(err, ErrEngineBusy) {
		t.Fatalf("StartOwnedEngine() error = %v, want ErrEngineBusy", err)
	}
}

func TestConcurrentStopReleasesOwnerExactlyOnce(t *testing.T) {
	var mu sync.Mutex
	stops := 0
	e := &Engine{stopPlatform: func(context.Context) error {
		mu.Lock()
		stops++
		mu.Unlock()
		return nil
	}}
	engineMu.Lock()
	previous := activeEngine
	activeEngine = e
	engineMu.Unlock()
	defer func() {
		engineMu.Lock()
		activeEngine = previous
		engineMu.Unlock()
	}()

	start := make(chan struct{})
	var wait sync.WaitGroup
	for range 16 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			e.Stop(context.Background())
		}()
	}
	close(start)
	wait.Wait()

	mu.Lock()
	gotStops := stops
	mu.Unlock()
	if gotStops != 1 {
		t.Fatalf("platform stops = %d, want 1", gotStops)
	}
	engineMu.Lock()
	owner := activeEngine
	engineMu.Unlock()
	if owner != nil {
		t.Fatal("owner remains reserved after its cleanup completed")
	}
}

func TestStaleStopCannotReleaseNewOwnerReservation(t *testing.T) {
	stale := &Engine{}
	current := &Engine{}
	engineMu.Lock()
	previous := activeEngine
	activeEngine = current
	engineMu.Unlock()
	defer func() {
		engineMu.Lock()
		activeEngine = previous
		engineMu.Unlock()
	}()

	stale.Stop(context.Background())
	engineMu.Lock()
	owner := activeEngine
	engineMu.Unlock()
	if owner != current {
		t.Fatal("stale cleanup released the newer generation reservation")
	}
}

func TestFailedEngineCleanupRetainsOwnerUntilRetrySucceeds(t *testing.T) {
	previous := activeEngine
	t.Cleanup(func() { activeEngine = previous })
	failure := errors.New("adapter still exists")
	attempts := 0
	e := &Engine{stopPlatform: func(context.Context) error {
		attempts++
		if attempts == 1 {
			return failure
		}
		return nil
	}}
	activeEngine = e
	if err := e.Stop(context.Background()); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if activeEngine != e {
		t.Fatal("failed cleanup lost engine ownership")
	}
	if _, err := StartOwnedEngine(platform_engine.EngineConfig{}, dnscache.New(), nil); !errors.Is(err, ErrEngineBusy) {
		t.Fatalf("new owner accepted before release: %v", err)
	}
	if err := e.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if activeEngine != nil {
		t.Fatal("successful cleanup retained engine ownership")
	}
}
