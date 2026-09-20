package sessionapi

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func newRecoveryTestManager(t *testing.T, runtime Runtime, now func() time.Time) (*Manager, string) {
	t.Helper()
	platform := &eventPlatform{events: make(chan StateChange, 128)}
	manager := NewManager(ManagerOptions{Runtime: runtime, Platform: platform, Now: now})
	id, err := currentSessionForTest(t, manager)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configureForTest(t, manager, id, fixture(t)); err != nil {
		t.Fatal(err)
	}
	return manager, id
}

func waitGenerationState(t *testing.T, manager *Manager, id string, minimumGeneration uint64, state State) SnapshotResult {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := manager.Snapshot(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Generation >= minimumGeneration && snapshot.State == state {
			return snapshot
		}
		time.Sleep(time.Millisecond)
	}
	snapshot, _ := manager.Snapshot(context.Background(), id)
	t.Fatalf("did not reach generation >= %d state %s: %#v", minimumGeneration, state, snapshot)
	return SnapshotResult{}
}

func TestAutoRecoveryStopsAfterThreeUnstableRecoveries(t *testing.T) {
	failures := make(chan struct{}, 1)
	runtime := &monitoringRuntime{failures: failures, stopped: make(chan uint64, 8)}
	manager, id := newRecoveryTestManager(t, runtime, time.Now)
	started, err := startForTest(t, manager, id, StartTarget{Mode: AutoSelect})
	if err != nil {
		t.Fatal(err)
	}
	waitGenerationState(t, manager, id, started.Generation, StateConnected)

	currentGeneration := started.Generation
	for retry := 1; retry <= autoRecoveryLimit; retry++ {
		failures <- struct{}{}
		snapshot := waitGenerationState(t, manager, id, currentGeneration+1, StateConnected)
		if !snapshot.Configured || snapshot.Recovering {
			t.Fatalf("recovered snapshot has invalid state: %#v", snapshot)
		}
		currentGeneration = snapshot.Generation
	}

	failures <- struct{}{}
	failed := waitGenerationState(t, manager, id, currentGeneration, StateFailed)
	if failed.Generation != currentGeneration || failed.LastFailure != FailureRuntime || failed.LastFailureMessage != autoRecoveryMessage || failed.Recovering {
		t.Fatalf("retry exhaustion snapshot = %#v", failed)
	}
	if again, snapshotErr := manager.Snapshot(context.Background(), id); snapshotErr != nil || again.Generation != currentGeneration {
		t.Fatalf("exhausted recovery started another generation: %#v, %v", again, snapshotErr)
	}

	// A deliberate new Start begins a fresh budget after exhaustion.
	started, err = startForTest(t, manager, id, StartTarget{Mode: AutoSelect})
	if err != nil {
		t.Fatalf("manual restart after exhaustion failed: %v", err)
	}
	currentGeneration = waitGenerationState(t, manager, id, started.Generation, StateConnected).Generation
	for retry := 1; retry <= autoRecoveryLimit; retry++ {
		failures <- struct{}{}
		snapshot := waitGenerationState(t, manager, id, currentGeneration+1, StateConnected)
		currentGeneration = snapshot.Generation
	}
}

func TestAutoRecoveryBudgetResetsAfterFiveStableMinutes(t *testing.T) {
	failures := make(chan struct{}, 1)
	runtime := &monitoringRuntime{failures: failures, stopped: make(chan uint64, 12)}
	var nowValue atomic.Pointer[time.Time]
	initial := time.Time{}
	nowValue.Store(&initial)
	now := func() time.Time { return *nowValue.Load() }
	manager, id := newRecoveryTestManager(t, runtime, now)
	started, err := startForTest(t, manager, id, StartTarget{Mode: AutoSelect})
	if err != nil {
		t.Fatal(err)
	}
	current := waitGenerationState(t, manager, id, started.Generation, StateConnected)
	for i := 0; i < autoRecoveryLimit; i++ {
		failures <- struct{}{}
		current = waitGenerationState(t, manager, id, current.Generation+1, StateConnected)
	}

	// Exactly five stable minutes replenish the budget before processing the
	// next health failure.
	afterStable := initial.Add(autoRecoveryStable)
	nowValue.Store(&afterStable)
	failures <- struct{}{}
	current = waitGenerationState(t, manager, id, current.Generation+1, StateConnected)
	for i := 0; i < autoRecoveryLimit-1; i++ {
		failures <- struct{}{}
		current = waitGenerationState(t, manager, id, current.Generation+1, StateConnected)
	}
	failures <- struct{}{}
	failed := waitGenerationState(t, manager, id, current.Generation, StateFailed)
	if failed.LastFailure != FailureRuntime || failed.LastFailureMessage != autoRecoveryMessage {
		t.Fatalf("post-reset retry exhaustion = %#v", failed)
	}
}

type blockedRecoveryRuntime struct {
	failures     chan struct{}
	probeEntered chan struct{}
}

func (r *blockedRecoveryRuntime) Probe(ctx context.Context, ref SessionRef, _ RuntimeProfile) (ProbeResult, error) {
	if ref.Generation == 1 {
		return ProbeResult{LatencyMillis: 1}, nil
	}
	select {
	case r.probeEntered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return ProbeResult{}, ctx.Err()
}

func (r *blockedRecoveryRuntime) Start(_ context.Context, ref SessionRef, _ RuntimeProfile) (RuntimeLease, error) {
	return monitoringRuntimeLease{generation: ref.Generation, failures: r.failures, stopped: make(chan uint64, 1)}, nil
}

func TestStopFromRecoverySnapshotCancelsReservedGeneration(t *testing.T) {
	runtime := &blockedRecoveryRuntime{failures: make(chan struct{}, 1), probeEntered: make(chan struct{}, 1)}
	manager, id := newRecoveryTestManager(t, runtime, time.Now)
	started, err := startForTest(t, manager, id, StartTarget{Mode: AutoSelect})
	if err != nil {
		t.Fatal(err)
	}
	waitGenerationState(t, manager, id, started.Generation, StateConnected)
	runtime.failures <- struct{}{}
	select {
	case <-runtime.probeEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("automatic recovery did not begin probing")
	}
	snapshot := waitGenerationState(t, manager, id, started.Generation+1, StateProbing)
	stopped, err := manager.Stop(context.Background(), id, started.Generation)
	if err != nil {
		t.Fatalf("stop using recovery snapshot generation failed: %v", err)
	}
	if stopped.Generation != snapshot.Generation {
		t.Fatalf("stop acknowledged generation %d, want current recovery generation %d", stopped.Generation, snapshot.Generation)
	}
	finished := waitGenerationState(t, manager, id, snapshot.Generation, StateIdle)
	if finished.Recovering {
		t.Fatalf("user stop left automatic recovery armed: %#v", finished)
	}
}

func TestStopFromRecoveryIdleCancelsReservedGeneration(t *testing.T) {
	manager, id := newRecoveryTestManager(t, &monitoringRuntime{stopped: make(chan uint64, 1)}, time.Now)
	s, err := manager.get(id)
	if err != nil {
		t.Fatal(err)
	}

	// Model the cleanup-complete IDLE snapshot published immediately before
	// startFailover reserves the next generation.
	s.mu.Lock()
	s.generation = 1
	s.state = StateIdle
	s.cleanupDone = true
	s.recovering = true
	s.recoveryOriginGeneration = 1
	s.mu.Unlock()

	stopped, err := manager.Stop(context.Background(), id, 1)
	if err != nil {
		t.Fatalf("stop from recovery IDLE snapshot failed: %v", err)
	}
	if stopped.Generation != 1 {
		t.Fatalf("stop acknowledged generation %d, want recovery origin generation 1", stopped.Generation)
	}
	manager.startFailover(s, 1)
	snapshot, err := manager.Snapshot(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Generation != 1 || snapshot.State != StateIdle || snapshot.Recovering {
		t.Fatalf("user stop from recovery IDLE allowed retry to start: %#v", snapshot)
	}
}

type recoveryOriginRuntime struct {
	failures              chan struct{}
	blockedStopGeneration uint64
	stopEntered           chan uint64
	releaseStop           chan struct{}
}

func (*recoveryOriginRuntime) Probe(context.Context, SessionRef, RuntimeProfile) (ProbeResult, error) {
	return ProbeResult{LatencyMillis: 1}, nil
}

func (r *recoveryOriginRuntime) Start(_ context.Context, ref SessionRef, _ RuntimeProfile) (RuntimeLease, error) {
	return recoveryOriginLease{generation: ref.Generation, runtime: r}, nil
}

type recoveryOriginLease struct {
	generation uint64
	runtime    *recoveryOriginRuntime
}

func (l recoveryOriginLease) HealthFailures() <-chan struct{} { return l.runtime.failures }

func (l recoveryOriginLease) Stop(context.Context) error {
	if l.generation == l.runtime.blockedStopGeneration {
		l.runtime.stopEntered <- l.generation
		<-l.runtime.releaseStop
	}
	return nil
}

func TestStopFromEarlierRecoveryCycleCannotStopLaterCycle(t *testing.T) {
	runtime := &recoveryOriginRuntime{
		failures:              make(chan struct{}, 1),
		blockedStopGeneration: 2,
		stopEntered:           make(chan uint64, 1),
		releaseStop:           make(chan struct{}, 1),
	}
	defer func() {
		select {
		case runtime.releaseStop <- struct{}{}:
		default:
		}
	}()
	manager, id := newRecoveryTestManager(t, runtime, time.Now)
	started, err := startForTest(t, manager, id, StartTarget{Mode: AutoSelect})
	if err != nil {
		t.Fatal(err)
	}
	waitGenerationState(t, manager, id, started.Generation, StateConnected)
	runtime.failures <- struct{}{}
	recovered := waitGenerationState(t, manager, id, started.Generation+1, StateConnected)

	runtime.failures <- struct{}{}
	select {
	case generation := <-runtime.stopEntered:
		if generation != recovered.Generation {
			t.Fatalf("teardown stopped generation %d, want %d", generation, recovered.Generation)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second health failure did not begin teardown")
	}
	if _, err := manager.Stop(context.Background(), id, started.Generation); CodeOf(err) != FailureStaleGeneration {
		t.Fatalf("stop from an earlier recovery cycle returned %v, want stale generation", err)
	}
	runtime.releaseStop <- struct{}{}
	waitGenerationState(t, manager, id, recovered.Generation+1, StateConnected)
}

type blockingHealthLeaseRuntime struct {
	failures    chan struct{}
	stopEntered chan uint64
	releaseStop chan struct{}
}

func (*blockingHealthLeaseRuntime) Probe(context.Context, SessionRef, RuntimeProfile) (ProbeResult, error) {
	return ProbeResult{LatencyMillis: 1}, nil
}

func (r *blockingHealthLeaseRuntime) Start(_ context.Context, ref SessionRef, _ RuntimeProfile) (RuntimeLease, error) {
	return blockingHealthLease{generation: ref.Generation, runtime: r}, nil
}

type blockingHealthLease struct {
	generation uint64
	runtime    *blockingHealthLeaseRuntime
}

func (l blockingHealthLease) HealthFailures() <-chan struct{} { return l.runtime.failures }

func (l blockingHealthLease) Stop(context.Context) error {
	l.runtime.stopEntered <- l.generation
	<-l.runtime.releaseStop
	return nil
}

func TestStopDuringHealthTeardownCancelsQueuedRecovery(t *testing.T) {
	runtime := &blockingHealthLeaseRuntime{
		failures:    make(chan struct{}, 1),
		stopEntered: make(chan uint64, 1),
		releaseStop: make(chan struct{}),
	}
	manager, id := newRecoveryTestManager(t, runtime, time.Now)
	started, err := startForTest(t, manager, id, StartTarget{Mode: AutoSelect})
	if err != nil {
		t.Fatal(err)
	}
	waitGenerationState(t, manager, id, started.Generation, StateConnected)
	runtime.failures <- struct{}{}
	select {
	case generation := <-runtime.stopEntered:
		if generation != started.Generation {
			t.Fatalf("teardown stopped generation %d, want %d", generation, started.Generation)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("health failure did not begin teardown")
	}
	stopping := waitGenerationState(t, manager, id, started.Generation, StateStopping)
	if !stopping.Recovering {
		t.Fatalf("health teardown lost recovery indication: %#v", stopping)
	}
	if _, err := manager.Stop(context.Background(), id, started.Generation); err != nil {
		t.Fatalf("user stop during teardown failed: %v", err)
	}
	close(runtime.releaseStop)
	idle := waitGenerationState(t, manager, id, started.Generation, StateIdle)
	if idle.Recovering {
		t.Fatalf("user stop left recovery pending: %#v", idle)
	}
	if again, err := manager.Snapshot(context.Background(), id); err != nil || again.Generation != started.Generation {
		t.Fatalf("teardown restarted despite user stop: %#v, %v", again, err)
	}
}
