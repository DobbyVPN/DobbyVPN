//go:build !(android || ios)

package runtime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunLockedWithPanicRecoveryIsBoundedAndUsesLockHeldCleanup(t *testing.T) {
	var mu sync.Mutex
	cleanupCalls := 0
	result := make(chan error, 1)
	go func() {
		result <- runLockedWithPanicRecovery(
			"test connect",
			&mu,
			func() error { panic("test panic") },
			func() error {
				cleanupCalls++
				return nil
			},
		)
	}()

	var err error
	select {
	case err = <-result:
	case <-time.After(time.Second):
		t.Fatal("locked panic recovery did not return")
	}
	if err == nil || !strings.Contains(err.Error(), "test connect panic") {
		t.Fatalf("panic recovery error = %v, want bounded panic error", err)
	}
	if cleanupCalls != 1 {
		t.Fatalf("cleanup calls = %d, want 1", cleanupCalls)
	}
	if !mu.TryLock() {
		t.Fatal("panic recovery did not release lifecycle mutex")
	}
	mu.Unlock()
}

func TestFinishRunIgnoresStaleGenerationCompletion(t *testing.T) {
	c := &nativeRuntime{state: statePreparing, generation: 12}

	c.finishRun(11, nil)
	if got := c.stateValue(); got != statePreparing {
		t.Fatalf("state after stale completion = %s, want %s", got, statePreparing)
	}
	if got := c.generationValue(); got != 12 {
		t.Fatalf("generation after stale completion = %d, want 12", got)
	}
}

func TestDisconnectReturnsRunCleanupFailure(t *testing.T) {
	want := errors.New("cleanup failed")
	cancelled := make(chan struct{})
	done := make(chan struct{})
	c := &nativeRuntime{state: statePreparing, generation: 3, cancel: func() { close(cancelled) }, done: done}
	disconnected := make(chan error, 1)
	go func() { disconnected <- c.Disconnect(context.Background()) }()

	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("Disconnect did not enter stopping state")
	}
	c.finishRun(3, want)
	close(done)
	select {
	case err := <-disconnected:
		if !errors.Is(err, want) {
			t.Fatalf("Disconnect error=%v, want %v", err, want)
		}
	case <-time.After(time.Second):
		t.Fatal("Disconnect did not return after failed cleanup")
	}
	if got := c.stateValue(); got != stateFailed {
		t.Fatalf("state after failed cleanup=%s, want %s", got, stateFailed)
	}
}

func TestDisconnectFromTerminalFailureDoesNotRemainStopping(t *testing.T) {
	want := errors.New("run failed")
	c := &nativeRuntime{state: stateFailed, generation: 8, runErr: want}

	if err := c.Disconnect(context.Background()); !errors.Is(err, want) {
		t.Fatalf("Disconnect error=%v, want %v", err, want)
	}
	if got := c.stateValue(); got != stateFailed {
		t.Fatalf("state=%s, want %s", got, stateFailed)
	}
}

func TestFinishRunReturnsStoppingGenerationToIdle(t *testing.T) {
	c := &nativeRuntime{state: stateStopping, generation: 4, done: make(chan struct{})}

	c.finishRun(4, nil)
	if got := c.stateValue(); got != stateIdle {
		t.Fatalf("state after stop completion = %s, want %s", got, stateIdle)
	}
}

func TestDisconnectRemainsStoppingUntilRunCleanupCompletes(t *testing.T) {
	cancelled := make(chan struct{})
	done := make(chan struct{})
	c := &nativeRuntime{
		state:      statePreparing,
		generation: 9,
		cancel:     func() { close(cancelled) },
		done:       done,
	}

	disconnected := make(chan error, 1)
	go func() { disconnected <- c.Disconnect(context.Background()) }()

	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("Disconnect did not cancel the active start")
	}
	if got := c.stateValue(); got != stateStopping {
		t.Fatalf("state before cleanup = %s, want %s", got, stateStopping)
	}
	select {
	case err := <-disconnected:
		t.Fatalf("Disconnect returned before cleanup: %v", err)
	default:
	}

	c.finishRun(9, nil)
	close(done)
	select {
	case err := <-disconnected:
		if err != nil {
			t.Fatalf("Disconnect(context.Context) error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Disconnect did not return after cleanup")
	}
	if got := c.stateValue(); got != stateIdle {
		t.Fatalf("state after cleanup = %s, want %s", got, stateIdle)
	}
}
