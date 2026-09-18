package ui

import (
	"context"
	"sync"
	"testing"
	"time"
)

type fakeMobileTransport struct {
	mu        sync.Mutex
	lastID    string
	snapshots []string
	configure string
	start     string
	stop      string
	reset     string
}

func (f *fakeMobileTransport) Configure(id string, _ int64, _ []byte) string {
	f.mu.Lock()
	f.lastID = id
	f.mu.Unlock()
	return f.configure
}
func (f *fakeMobileTransport) Start(id string, _ int64, _ string, _ int32) string {
	f.mu.Lock()
	f.lastID = id
	f.mu.Unlock()
	return f.start
}
func (f *fakeMobileTransport) Stop(id string, _ int64) string {
	f.mu.Lock()
	f.lastID = id
	f.mu.Unlock()
	return f.stop
}
func (f *fakeMobileTransport) Snapshot(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastID = id
	if len(f.snapshots) == 0 {
		return `{"ok":true,"result":{"session_id":"session-1","sequence":1,"state":"IDLE"}}`
	}
	value := f.snapshots[0]
	f.snapshots = f.snapshots[1:]
	return value
}
func (f *fakeMobileTransport) Reset(id string, _ int64) string {
	f.mu.Lock()
	f.lastID = id
	f.mu.Unlock()
	return f.reset
}

func TestMobileClientUsesOneOpaqueSessionAcrossOperations(t *testing.T) {
	transport := &fakeMobileTransport{
		configure: `{"ok":true,"result":{"digest":"digest","sequence":2,"source_kind":"inline","profiles":[],"warnings":[]}}`,
		start:     `{"ok":true,"result":{"generation":4,"sequence":3}}`,
		stop:      `{"ok":true,"result":{"generation":4,"sequence":4}}`,
		reset:     `{"ok":true,"result":{"session_id":"session-1","sequence":5,"state":"IDLE"}}`,
	}
	client := NewMobileClientWithTransport(transport)
	if _, err := client.Snapshot(context.Background()); err != nil {
		t.Fatalf("initial snapshot: %v", err)
	}
	if _, err := client.Configure(context.Background(), []byte("inline"), 1); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if _, err := client.Start(context.Background(), 2); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := client.Stop(context.Background(), 4); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, err := client.Reset(context.Background(), 4); err != nil {
		t.Fatalf("reset: %v", err)
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.lastID != "session-1" {
		t.Fatalf("last session id = %q, want session-1", transport.lastID)
	}
}

func TestMobileClientWatchCoalescesAndStopsWithContext(t *testing.T) {
	transport := &fakeMobileTransport{snapshots: []string{
		`{"ok":true,"result":{"session_id":"session-1","sequence":1,"state":"IDLE"}}`,
		`{"ok":true,"result":{"session_id":"session-1","sequence":2,"state":"CONNECTED","generation":1}}`,
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates, err := NewMobileClientWithTransport(transport).Watch(ctx)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	select {
	case first := <-updates:
		if first.State != StateIdle {
			t.Fatalf("first state = %q", first.State)
		}
	case <-time.After(time.Second):
		t.Fatal("watch did not emit an initial snapshot")
	}
	cancel()
	select {
	case _, ok := <-updates:
		if ok {
			// A queued latest snapshot is valid; the channel must still close.
			select {
			case _, ok = <-updates:
			case <-time.After(time.Second):
				t.Fatal("watch did not close after cancellation")
			}
		}
		if ok {
			t.Fatal("watch remained open after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("watch did not close after cancellation")
	}
}

func TestMobileClientRejectsCancelledRequests(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := NewMobileClientWithTransport(&fakeMobileTransport{})
	if _, err := client.Snapshot(ctx); err == nil {
		t.Fatal("cancelled snapshot unexpectedly called the transport")
	}
}
