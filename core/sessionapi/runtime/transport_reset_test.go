package runtime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"core/sessionapi"
)

func useTransportResetRecheck(t *testing.T, wait time.Duration) {
	t.Helper()
	old := transportResetRecheck
	transportResetRecheck = wait
	t.Cleanup(func() { transportResetRecheck = old })
}

func stopHealthMonitor(t *testing.T, l *lease) {
	t.Helper()
	l.healthCancel()
	<-l.healthDone
}

// A short network drop leaves a multiplexed transport (Xray gRPC, XHTTP) on a
// dead connection: every check fails until its pooled connections are
// dropped. The first failed check drops them and checks again soon, so the
// tunnel recovers within a second instead of after three full intervals, and
// the runtime is not restarted.
func TestMultiplexedTransportRecoversAfterFirstFailedCheck(t *testing.T) {
	useTransportResetRecheck(t, 20*time.Millisecond)
	var resets atomic.Int32
	var checks atomic.Int32
	recovered := make(chan struct{})
	var once sync.Once
	l := &lease{resetTransport: func(string) { resets.Add(1) }}
	check := func(context.Context, sessionapi.SessionRef, string) error {
		checks.Add(1)
		if resets.Load() == 0 {
			return errors.New("tunnel probe reached 0/3 successful endpoints")
		}
		once.Do(func() { close(recovered) })
		return nil
	}
	started := time.Now()
	l.startHealthMonitor(context.Background(), sessionapi.SessionRef{Generation: 1}, "", check, time.Hour, 3)
	t.Cleanup(func() { stopHealthMonitor(t, l) })
	select {
	case <-recovered:
	case err := <-l.healthFailed:
		t.Fatalf("the runtime must not be restarted: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("no recovery after the first failed check")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("recovered after %s, want well under the health interval", elapsed)
	}
	if resets.Load() != 1 || checks.Load() != 2 {
		t.Fatalf("resets=%d checks=%d, want one reset and one early recheck", resets.Load(), checks.Load())
	}
}

// A per-flow transport (Outline) has no pooled connections: a failed check
// waits for the normal interval as before.
func TestPerFlowTransportKeepsHealthInterval(t *testing.T) {
	useTransportResetRecheck(t, time.Millisecond)
	var checks atomic.Int32
	l := &lease{}
	check := func(context.Context, sessionapi.SessionRef, string) error {
		checks.Add(1)
		return errors.New("probe failed")
	}
	l.startHealthMonitor(context.Background(), sessionapi.SessionRef{Generation: 1}, "", check, time.Hour, 3)
	t.Cleanup(func() { stopHealthMonitor(t, l) })
	time.Sleep(100 * time.Millisecond)
	if n := checks.Load(); n != 1 {
		t.Fatalf("checks=%d, want 1: a per-flow transport must keep the health interval", n)
	}
}

// A reported network event drops the pooled connections before the
// immediate check, so the check already runs on new connections.
func TestNetworkEventDropsMultiplexedConnectionsBeforeCheck(t *testing.T) {
	var mu sync.Mutex
	var order []string
	checked := make(chan struct{}, 4)
	l := &lease{resetTransport: func(reason string) {
		mu.Lock()
		order = append(order, "reset: "+reason)
		mu.Unlock()
	}}
	check := func(context.Context, sessionapi.SessionRef, string) error {
		mu.Lock()
		order = append(order, "check")
		mu.Unlock()
		checked <- struct{}{}
		return nil
	}
	l.startHealthMonitor(context.Background(), sessionapi.SessionRef{Generation: 1}, "", check, time.Hour, 3)
	t.Cleanup(func() { stopHealthMonitor(t, l) })
	<-checked
	l.ProbeNow("network_path_changed")
	<-checked
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 || order[0] != "check" || !strings.HasPrefix(order[1], "reset: network event") || order[2] != "check" {
		t.Fatalf("order = %v, want check, reset on the network event, check", order)
	}
}
