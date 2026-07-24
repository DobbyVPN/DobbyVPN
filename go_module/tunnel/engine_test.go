package tunnel

import (
	"errors"
	"sync"
	"testing"

	"go_module/tunnel/platform_engine"
)

func TestOwnedEngineStopIsIdempotent(t *testing.T) {
	stops := 0
	e := &Engine{ready: true, stopPlatform: func() { stops++ }}
	engineMu.Lock()
	previous := activeEngine
	activeEngine = e
	engineMu.Unlock()
	defer func() {
		engineMu.Lock()
		activeEngine = previous
		engineMu.Unlock()
	}()

	e.Stop()
	e.Stop()
	if stops != 1 {
		t.Fatalf("platform stops = %d, want 1", stops)
	}
	if e.Ready() {
		t.Fatal("stopped engine still reports ready")
	}
}

func TestStaleEngineCannotStopCurrentOwner(t *testing.T) {
	oldStops, currentStops := 0, 0
	old := &Engine{ready: true, stopPlatform: func() { oldStops++ }}
	current := &Engine{ready: true, stopPlatform: func() { currentStops++ }}
	engineMu.Lock()
	previous := activeEngine
	activeEngine = current
	engineMu.Unlock()
	defer func() {
		engineMu.Lock()
		activeEngine = previous
		engineMu.Unlock()
	}()

	old.Stop()
	if oldStops != 0 || currentStops != 0 {
		t.Fatalf("stale stop affected platform: old=%d current=%d", oldStops, currentStops)
	}
	if !current.Ready() {
		t.Fatal("current owner was marked not ready by stale stop")
	}
}

func TestSecondOwnedStartReturnsBusyBeforeTouchingPlatform(t *testing.T) {
	engineMu.Lock()
	previous := activeEngine
	activeEngine = &Engine{ready: true}
	engineMu.Unlock()
	defer func() {
		engineMu.Lock()
		activeEngine = previous
		engineMu.Unlock()
	}()

	_, err := StartOwnedEngine(platform_engine.EngineConfig{})
	if !errors.Is(err, ErrEngineBusy) {
		t.Fatalf("StartOwnedEngine() error = %v, want ErrEngineBusy", err)
	}
}

func TestConcurrentStopReleasesOwnerExactlyOnce(t *testing.T) {
	var mu sync.Mutex
	stops := 0
	e := &Engine{ready: true, statsStop: make(chan struct{}), stopPlatform: func() {
		mu.Lock()
		stops++
		mu.Unlock()
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
			e.Stop()
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
	if e.Ready() {
		t.Fatal("stopped engine remains ready")
	}
}

func TestStaleStopCannotReleaseNewOwnerReservation(t *testing.T) {
	stale := &Engine{ready: true}
	current := &Engine{ready: true}
	engineMu.Lock()
	previous := activeEngine
	activeEngine = current
	engineMu.Unlock()
	defer func() {
		engineMu.Lock()
		activeEngine = previous
		engineMu.Unlock()
	}()

	stale.Stop()
	engineMu.Lock()
	owner := activeEngine
	engineMu.Unlock()
	if owner != current {
		t.Fatal("stale cleanup released the newer generation reservation")
	}
}
