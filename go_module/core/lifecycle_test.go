//go:build !(android || ios)

package core

import (
	"errors"
	"testing"
	"time"
)

func TestFinishRunIgnoresStaleGenerationCompletion(t *testing.T) {
	c := &CoreClient{state: StatePreparing, generation: 12}

	c.finishRun(11, nil)
	if got := c.State(); got != StatePreparing {
		t.Fatalf("state after stale completion = %s, want %s", got, StatePreparing)
	}
	if got := c.Generation(); got != 12 {
		t.Fatalf("generation after stale completion = %d, want 12", got)
	}
}

func TestDisconnectReturnsRunCleanupFailure(t *testing.T) {
	want := errors.New("cleanup failed")
	cancelled := make(chan struct{})
	done := make(chan struct{})
	c := &CoreClient{state: StatePreparing, generation: 3, cancel: func() { close(cancelled) }, done: done}
	disconnected := make(chan error, 1)
	go func() { disconnected <- c.Disconnect() }()

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
	if got := c.State(); got != StateFailed {
		t.Fatalf("state after failed cleanup=%s, want %s", got, StateFailed)
	}
}

func TestDisconnectFromTerminalFailureDoesNotRemainStopping(t *testing.T) {
	want := errors.New("run failed")
	c := &CoreClient{state: StateFailed, generation: 8, runErr: want}

	if err := c.Disconnect(); !errors.Is(err, want) {
		t.Fatalf("Disconnect error=%v, want %v", err, want)
	}
	if got := c.State(); got != StateFailed {
		t.Fatalf("state=%s, want %s", got, StateFailed)
	}
}

func TestFinishRunReturnsStoppingGenerationToIdle(t *testing.T) {
	c := &CoreClient{state: StateStopping, generation: 4, done: make(chan struct{})}

	c.finishRun(4, nil)
	if got := c.State(); got != StateIdle {
		t.Fatalf("state after stop completion = %s, want %s", got, StateIdle)
	}
}

func TestDisconnectRemainsStoppingUntilRunCleanupCompletes(t *testing.T) {
	cancelled := make(chan struct{})
	done := make(chan struct{})
	c := &CoreClient{
		state:      StatePreparing,
		generation: 9,
		cancel:     func() { close(cancelled) },
		done:       done,
	}

	disconnected := make(chan error, 1)
	go func() { disconnected <- c.Disconnect() }()

	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("Disconnect did not cancel the active start")
	}
	if got := c.State(); got != StateStopping {
		t.Fatalf("state before cleanup = %s, want %s", got, StateStopping)
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
			t.Fatalf("Disconnect() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Disconnect did not return after cleanup")
	}
	if got := c.State(); got != StateIdle {
		t.Fatalf("state after cleanup = %s, want %s", got, StateIdle)
	}
}
