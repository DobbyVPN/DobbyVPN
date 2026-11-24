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

// The health failure tells how long the attempt was healthy before the
// failing checks, so the session can tell a transient failure from a server
// that never stays healthy.
func TestHealthFailureReportsHealthyChecksBeforeFailing(t *testing.T) {
	var checks atomic.Int32
	l := &lease{}
	check := func(context.Context, sessionapi.SessionRef, string) error {
		if checks.Add(1) <= 2 {
			return nil
		}
		return errors.New("probe failed")
	}
	l.startHealthMonitor(context.Background(), sessionapi.SessionRef{Generation: 1}, "", check, time.Millisecond, 3)
	t.Cleanup(func() { stopHealthMonitor(t, l) })
	select {
	case cause := <-l.healthFailed:
		var health *sessionapi.HealthFailure
		if !errors.As(cause, &health) || health.HealthyChecks != 2 || health.HealthyFor <= 0 {
			t.Fatalf("want 2 healthy checks before failing, got %#v", cause)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no health failure")
	}
}

// The health failure says whether server lookups saw the physical network
// missing or replaced since the attempt was last known healthy.
func TestHealthFailureReportsObservedNetworkLoss(t *testing.T) {
	for _, lost := range []bool{true, false} {
		var asked time.Time
		var checks atomic.Int32
		l := &lease{networkLostSince: func(since time.Time) bool { asked = since; return lost }}
		check := func(context.Context, sessionapi.SessionRef, string) error {
			if checks.Add(1) == 1 {
				return nil
			}
			return errors.New("probe failed")
		}
		started := time.Now()
		l.startHealthMonitor(context.Background(), sessionapi.SessionRef{Generation: 1}, "", check, time.Millisecond, 2)
		select {
		case cause := <-l.healthFailed:
			var health *sessionapi.HealthFailure
			if !errors.As(cause, &health) || health.NetworkLost != lost {
				t.Fatalf("lost=%t: got %#v", lost, cause)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no health failure")
		}
		stopHealthMonitor(t, l)
		if asked.Before(started) {
			t.Fatalf("the loss must be read from the last healthy check, asked since %v (monitor started %v)", asked, started)
		}
	}
}

// scriptedCheck is one connected health check in a monitor script: its result,
// and whether a network event arrives right after it (the check queues it, so
// the monitor sees it before its next timer).
type scriptedCheck struct {
	ok    bool
	event bool
}

// The recovery rules are decided from the monitor's real check counting, on a
// fake clock that moves 10 s per check: an attempt proves healthy once two or
// more consecutive scheduled checks span 30 s (event-triggered checks and
// early rechecks after a transport reset do not count, a failing scheduled
// check starts the window over), it stays proven for the rest of the
// attempt, and a network event exempts a failure only if no check passed
// after it.
func TestHealthFailureCountsOnlyScheduledChecks(t *testing.T) {
	useTransportResetRecheck(t, time.Millisecond)
	pass, fail := scriptedCheck{ok: true}, scriptedCheck{}
	passThenEvent, failThenEvent := scriptedCheck{ok: true, event: true}, scriptedCheck{event: true}
	for _, test := range []struct {
		name        string
		script      []scriptedCheck
		reset       bool
		threshold   int
		checks      int
		proved      bool
		networkLost bool
	}{
		{name: "four scheduled checks over 30 s", script: []scriptedCheck{pass, pass, pass, pass, fail, fail}, threshold: 2, checks: 4, proved: true},
		{name: "two scheduled checks within 10 s", script: []scriptedCheck{pass, pass, fail, fail}, threshold: 2, checks: 2},
		{name: "proved earlier, short run before the failures", script: []scriptedCheck{pass, pass, pass, pass, fail, pass, fail, fail}, threshold: 2, checks: 1, proved: true},
		{name: "a failing scheduled check inside the window", script: []scriptedCheck{pass, pass, pass, fail, pass, pass, pass, fail, fail}, threshold: 2, checks: 3},
		{name: "event-triggered checks do not count", script: []scriptedCheck{passThenEvent, passThenEvent, passThenEvent, pass, fail, fail}, threshold: 2, checks: 1},
		{name: "rechecks after a reset do not count", script: []scriptedCheck{pass, fail, pass, pass, fail, fail}, reset: true, threshold: 2, checks: 1},
		{name: "event survived by a passing check", script: []scriptedCheck{passThenEvent, pass, fail, fail}, threshold: 2, checks: 1},
		{name: "event right before the failures", script: []scriptedCheck{pass, passThenEvent, fail, fail}, threshold: 2, checks: 2, networkLost: true},
		{name: "event during the failures", script: []scriptedCheck{pass, pass, failThenEvent, fail}, threshold: 2, checks: 2, networkLost: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var clock atomic.Int64 // seconds on the fake clock
			var calls atomic.Int32
			l := &lease{now: func() time.Time { return time.Unix(clock.Load(), 0) }}
			if test.reset {
				l.resetTransport = func(string) {}
			}
			check := func(context.Context, sessionapi.SessionRef, string) error {
				index := int(calls.Add(1)) - 1
				if index > 0 {
					clock.Add(10)
				}
				step := test.script[min(index, len(test.script)-1)]
				if step.event {
					l.ProbeNow("test event")
				}
				if step.ok {
					return nil
				}
				return errors.New("probe failed")
			}
			l.startHealthMonitor(context.Background(), sessionapi.SessionRef{Generation: 1}, "", check, 5*time.Millisecond, test.threshold)
			var cause error
			select {
			case cause = <-l.healthFailed:
			case <-time.After(5 * time.Second):
				t.Fatal("no health failure")
			}
			stopHealthMonitor(t, l)
			var health *sessionapi.HealthFailure
			if !errors.As(cause, &health) {
				t.Fatalf("cause %T is not a HealthFailure", cause)
			}
			if int(calls.Load()) != len(test.script) {
				t.Fatalf("ran %d checks, script has %d", calls.Load(), len(test.script))
			}
			if health.HealthyChecks != test.checks || health.ProvedHealthy() != test.proved || health.NetworkLost != test.networkLost {
				t.Fatalf("got checks=%d for=%s proved=%t network_lost=%t, want checks=%d proved=%t network_lost=%t",
					health.HealthyChecks, health.HealthyFor, health.ProvedHealthy(), health.NetworkLost, test.checks, test.proved, test.networkLost)
			}
		})
	}
}
