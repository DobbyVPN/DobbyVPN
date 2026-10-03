package sessionapi

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestShutdownFencesAcquisitionUntilLateLeaseReleased(t *testing.T) {
	entered, release, stopped := make(chan struct{}, 1), make(chan struct{}), make(chan struct{}, 1)
	m := NewManager(ManagerOptions{Runtime: blockingStartRuntime{entered: entered, release: release, stopped: stopped}})
	id := configured(t, m)
	if _, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex}); err != nil {
		t.Fatal(err)
	}
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.StopAndWait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown = %v", err)
	}
	if snapshot := snapshotForTest(t, m, id); snapshot.CleanupComplete || snapshot.State != StateStopping {
		t.Fatalf("pending snapshot = %#v", snapshot)
	}
	if err := m.Resume(); CodeOf(err) != FailureCleanup {
		t.Fatalf("resume while pending = %v", err)
	}
	if _, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex}); CodeOf(err) != FailureConflict {
		t.Fatalf("start during shutdown = %v", err)
	}
	close(release)
	if err := m.StopAndWait(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("shutdown preceded resource release")
	}
	if _, err := configureForTest(t, m, id, fixture(t)); CodeOf(err) != FailureConflict {
		t.Fatalf("closed manager accepted configure: %v", err)
	}
	if err := m.Resume(); err != nil {
		t.Fatal(err)
	}
	if _, err := configureForTest(t, m, id, fixture(t)); err != nil {
		t.Fatal(err)
	}
}

func TestShutdownReportsReleaseFailureAndRemainsClosed(t *testing.T) {
	m := NewManager(ManagerOptions{Runtime: &cleanupErrorRuntime{err: errors.New("native release sentinel")}})
	id := configured(t, m)
	if _, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex}); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, id, StateConnected)
	for range 2 {
		if err := m.StopAndWait(context.Background()); CodeOf(err) != FailureCleanup {
			t.Fatalf("shutdown = %v", err)
		}
		if snapshot := snapshotForTest(t, m, id); snapshot.CleanupComplete {
			t.Fatalf("failed release reported complete: %#v", snapshot)
		}
	}
	if err := m.Resume(); CodeOf(err) != FailureCleanup {
		t.Fatalf("resume after failure = %v", err)
	}
}

func TestCleanupContextSharesOneDeadlineAfterCancellation(t *testing.T) {
	budget := &cleanupBudget{}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), cleanupBudgetKey{}, budget))
	defer cancel()
	first := CleanupContext(ctx)
	cancel()
	second := CleanupContext(ctx)
	if first != second || first.Err() != nil {
		t.Fatal("acquisition cancellation replaced or canceled cleanup budget")
	}
	deadline, ok := second.Deadline()
	if !ok || time.Until(deadline) > CleanupTimeout {
		t.Fatalf("cleanup deadline = %v", deadline)
	}
	budget.cancel()
	if CleanupContext(first) != first {
		t.Fatal("expired cleanup budget was extended")
	}
}

type retryCleanupRuntime struct{ lease *retryCleanupLease }

func (r retryCleanupRuntime) Start(context.Context, SessionRef, RuntimeProfile) (RuntimeLease, error) {
	return r.lease, nil
}

type retryCleanupLease struct{ calls int }

func (l *retryCleanupLease) Stop(context.Context) error {
	l.calls++
	if l.calls == 1 {
		return errors.New("OS operation still pending")
	}
	return nil
}

func TestRepeatedStopRetriesRetainedOwnerBeforeReleasingPlatform(t *testing.T) {
	lease := &retryCleanupLease{}
	platform := &fakePlatform{}
	m := NewManager(ManagerOptions{Runtime: retryCleanupRuntime{lease}, Platform: platform})
	id := configured(t, m)
	start, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, id, StateConnected)
	if _, err := m.Stop(context.Background(), id, start.Generation); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, id, StateFailed)
	if snapshotForTest(t, m, id).CleanupComplete {
		t.Fatal("failed release was reported complete")
	}
	if _, err := m.Stop(context.Background(), id, start.Generation); err != nil {
		t.Fatal(err)
	}
	if got := waitState(t, m, id, StateIdle); !got.CleanupComplete {
		t.Fatalf("retry snapshot=%#v", got)
	}
	if lease.calls != 2 {
		t.Fatalf("release calls=%d", lease.calls)
	}
}
