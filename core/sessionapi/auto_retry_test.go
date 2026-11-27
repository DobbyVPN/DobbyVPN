package sessionapi

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

const retryTestConfig = "[[Outline]]\nServer = 'vpn.invalid'\nPort = 443\nPassword = 'synthetic'\n\n"

// manualTimers records each scheduled retry so a test fires it at once.
type manualTimers struct {
	mu      sync.Mutex
	delays  []time.Duration
	fires   []func()
	stopped []bool
}

func (t *manualTimers) after(d time.Duration, f func()) func() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	index := len(t.fires)
	t.delays, t.fires, t.stopped = append(t.delays, d), append(t.fires, f), append(t.stopped, false)
	return func() bool {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.stopped[index] = true
		return true
	}
}

func (t *manualTimers) last() (time.Duration, func(), bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := len(t.fires)
	if n == 0 {
		return 0, nil, false
	}
	return t.delays[n-1], t.fires[n-1], t.stopped[n-1]
}

func (t *manualTimers) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.fires)
}

// switchRuntime fails every start with err until err is nil. While block is
// set, a start waits for it to close or for its cancellation.
type switchRuntime struct {
	mu     sync.Mutex
	err    error
	starts int
	block  chan struct{}
}

func (r *switchRuntime) Start(ctx context.Context, _ SessionRef, _ RuntimeProfile) (RuntimeLease, error) {
	r.mu.Lock()
	r.starts++
	block, err := r.block, r.err
	r.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	return noopRuntimeLease{}, nil
}

func (r *switchRuntime) setBlock(block chan struct{}) {
	r.mu.Lock()
	r.block = block
	r.mu.Unlock()
}

func (r *switchRuntime) set(err error) {
	r.mu.Lock()
	r.err = err
	r.mu.Unlock()
}

func (r *switchRuntime) startCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.starts
}

type noopRuntimeLease struct{}

func (noopRuntimeLease) Stop(context.Context) error { return nil }

var retryTestNow = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

func newRetryManager(t *testing.T, runtime Runtime) (*Manager, *manualTimers, string) {
	t.Helper()
	m := NewManager(ManagerOptions{Runtime: runtime, Now: func() time.Time { return retryTestNow }})
	timers := &manualTimers{}
	m.afterFunc = timers.after
	snapshot, err := m.Snapshot(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Configure(context.Background(), snapshot.SessionID, snapshot.Sequence, []byte(retryTestConfig)); err != nil {
		t.Fatal(err)
	}
	return m, timers, snapshot.SessionID
}

func startForRetry(t *testing.T, m *Manager, id string, target StartTarget) StartResult {
	t.Helper()
	snapshot, err := m.Snapshot(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	result, err := m.Start(context.Background(), id, snapshot.Sequence, target)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func waitForSnapshot(t *testing.T, m *Manager, id string, want func(SnapshotResult) bool) SnapshotResult {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		snapshot, err := m.Snapshot(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if want(snapshot) {
			return snapshot
		}
		if time.Now().After(deadline) {
			t.Fatalf("snapshot did not reach the expected state: %+v", snapshot)
		}
		time.Sleep(time.Millisecond)
	}
}

func failedWithRetry(s SnapshotResult) bool {
	return s.State == StateFailed && !s.NextRetryAt.IsZero()
}

func TestAutoRetryPausesGrowAndResetAfterAConnection(t *testing.T) {
	runtime := &switchRuntime{err: errors.New("readiness probe failed")}
	m, timers, id := newRetryManager(t, runtime)
	startForRetry(t, m, id, StartTarget{Mode: AutoSelect})
	want := []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 2 * time.Minute}
	for i, delay := range want {
		snapshot := waitForSnapshot(t, m, id, func(s SnapshotResult) bool { return failedWithRetry(s) && timers.count() == i+1 })
		got, fire, _ := timers.last()
		if got != delay {
			t.Fatalf("pause %d = %s, want %s", i+1, got, delay)
		}
		if !snapshot.NextRetryAt.Equal(retryTestNow.Add(delay)) || snapshot.LastFailure != FailureProbe || !snapshot.VPNActive {
			t.Fatalf("snapshot after failure %d = %+v", i+1, snapshot)
		}
		if i < len(want)-1 {
			fire()
		}
	}
	runtime.set(nil)
	_, fire, _ := timers.last()
	fire()
	connected := waitForSnapshot(t, m, id, func(s SnapshotResult) bool { return s.State == StateConnected })
	if !connected.NextRetryAt.IsZero() {
		t.Fatalf("connected snapshot still has a retry time: %+v", connected)
	}
	s := m.session
	retryCount := func() int {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.autoRetryCount
	}
	if retryCount() == 0 {
		t.Fatal("the pauses were reset before the connection proved healthy")
	}
	// The connection stays up for the proven-healthy period.
	delay, healthy, _ := timers.last()
	if delay != autoRetryProvenHealthy {
		t.Fatalf("proven-healthy timer = %s, want %s", delay, autoRetryProvenHealthy)
	}
	healthy()
	if retryCount() != 0 {
		t.Fatalf("retry count after a healthy connection = %d, want 0", retryCount())
	}
	if runtime.startCount() != len(want)+1 {
		t.Fatalf("runtime starts = %d, want %d", runtime.startCount(), len(want)+1)
	}
}

func TestAutoRetryIsCanceledByStop(t *testing.T) {
	runtime := &switchRuntime{err: errors.New("readiness probe failed")}
	m, timers, id := newRetryManager(t, runtime)
	started := startForRetry(t, m, id, StartTarget{Mode: AutoSelect})
	failed := waitForSnapshot(t, m, id, failedWithRetry)
	if _, err := m.Stop(context.Background(), id, failed.Generation); err != nil {
		t.Fatal(err)
	}
	_, fire, stopped := timers.last()
	if !stopped {
		t.Fatal("Stop did not cancel the scheduled retry")
	}
	waitForSnapshot(t, m, id, func(s SnapshotResult) bool { return s.State == StateIdle && s.NextRetryAt.IsZero() })
	starts := runtime.startCount()
	fire() // A timer that already fired must not start anything.
	time.Sleep(20 * time.Millisecond)
	if runtime.startCount() != starts {
		t.Fatalf("a retry ran after Stop (generation %d)", started.Generation)
	}
}

func TestAutoRetryIsCanceledByAChangedSubscription(t *testing.T) {
	runtime := &switchRuntime{err: errors.New("readiness probe failed")}
	m, timers, id := newRetryManager(t, runtime)
	startForRetry(t, m, id, StartTarget{Mode: AutoSelect})
	failed := waitForSnapshot(t, m, id, failedWithRetry)
	// The same subscription loaded again keeps the schedule.
	if _, err := m.Configure(context.Background(), id, failed.Sequence, []byte(retryTestConfig)); err != nil {
		t.Fatal(err)
	}
	if _, _, stopped := timers.last(); stopped {
		t.Fatal("reloading the same subscription canceled the retry")
	}
	snapshot, _ := m.Snapshot(context.Background(), id)
	changed := retryTestConfig + "[[Outline]]\nServer = 'other.invalid'\nPort = 443\nPassword = 'synthetic'\n\n"
	if _, err := m.Configure(context.Background(), id, snapshot.Sequence, []byte(changed)); err != nil {
		t.Fatal(err)
	}
	_, fire, stopped := timers.last()
	if !stopped {
		t.Fatal("a changed subscription did not cancel the retry")
	}
	starts := runtime.startCount()
	fire()
	time.Sleep(20 * time.Millisecond)
	if runtime.startCount() != starts {
		t.Fatal("a retry ran after the subscription changed")
	}
	if after, _ := m.Snapshot(context.Background(), id); !after.NextRetryAt.IsZero() {
		t.Fatalf("snapshot still has a retry time: %+v", after)
	}
}

func TestAutoRetryIsCanceledByAUserStart(t *testing.T) {
	runtime := &switchRuntime{err: errors.New("readiness probe failed")}
	m, timers, id := newRetryManager(t, runtime)
	startForRetry(t, m, id, StartTarget{Mode: AutoSelect})
	waitForSnapshot(t, m, id, failedWithRetry)
	startForRetry(t, m, id, StartTarget{Mode: AutoSelect}) // the user presses Retry
	if _, _, stopped := timers.last(); !stopped {
		t.Fatal("a user Retry did not cancel the scheduled retry")
	}
	waitForSnapshot(t, m, id, func(s SnapshotResult) bool { return failedWithRetry(s) && timers.count() == 2 })
	if delay, _, _ := timers.last(); delay != 5*time.Second {
		t.Fatalf("pause after a user Retry = %s, want the schedule from the start", delay)
	}
}

func TestNoAutoRetryForFailuresARetryCannotFix(t *testing.T) {
	runtime := &switchRuntime{err: failure(FailurePlatform, "VPN permission was revoked")}
	m, timers, id := newRetryManager(t, runtime)
	startForRetry(t, m, id, StartTarget{Mode: ProfileIndex, Index: 0})
	snapshot := waitForSnapshot(t, m, id, func(s SnapshotResult) bool { return s.State == StateFailed })
	if snapshot.LastFailure != FailurePlatform || !snapshot.NextRetryAt.IsZero() || timers.count() != 0 {
		t.Fatalf("platform failure scheduled a retry: %+v, timers %d", snapshot, timers.count())
	}
	for _, code := range []FailureCode{
		FailureInvalidArgument, FailureNotFound, FailureConflict, FailureNotConfigured, FailureStaleGeneration,
		FailureUnsupported, FailureMalformedConfig, FailurePlatform, FailureCanceled, FailureInternal, FailureCleanup,
	} {
		if autoRetryable(code) {
			t.Errorf("%s is retried automatically", code)
		}
	}
	for _, code := range []FailureCode{FailureRuntime, FailureProbe} {
		if !autoRetryable(code) {
			t.Errorf("%s is not retried automatically", code)
		}
	}
}

// healthRuntime connects with a lease whose health checks the test fails,
// then fails every later start while err is set.
type healthRuntime struct {
	switchRuntime
	health chan error
}

type healthLease struct{ failures chan error }

func (healthLease) Stop(context.Context) error     { return nil }
func (l healthLease) HealthFailures() <-chan error { return l.failures }

func (r *healthRuntime) Start(ctx context.Context, ref SessionRef, profile RuntimeProfile) (RuntimeLease, error) {
	if _, err := r.switchRuntime.Start(ctx, ref, profile); err != nil {
		return nil, err
	}
	return healthLease{failures: r.health}, nil
}

// The reported case: a connection loses its resolver, the immediate
// reconnect fails as well, and the session is FAILED with the tunnel kept.
func TestAutoRetryAfterHealthFailureAndFailedReconnect(t *testing.T) {
	runtime := &healthRuntime{health: make(chan error, 1)}
	m, timers, id := newRetryManager(t, runtime)
	startForRetry(t, m, id, StartTarget{Mode: AutoSelect})
	waitForSnapshot(t, m, id, func(s SnapshotResult) bool { return s.State == StateConnected })
	runtime.set(errors.New("readiness probe failed"))
	runtime.health <- errors.New("connected health check failed")
	snapshot := waitForSnapshot(t, m, id, failedWithRetry)
	if !snapshot.VPNActive || !snapshot.CanRetry || snapshot.LastFailure != FailureProbe {
		t.Fatalf("snapshot after the failed reconnect = %+v", snapshot)
	}
	if delay, _, _ := timers.last(); delay != 5*time.Second {
		t.Fatalf("first pause = %s, want 5s", delay)
	}
	runtime.set(nil)
	_, fire, _ := timers.last()
	fire()
	waitForSnapshot(t, m, id, func(s SnapshotResult) bool { return s.State == StateConnected && s.NextRetryAt.IsZero() })
}

// A user Stop sent with the FAILED generation still stops the session after
// an automatic retry has started a newer generation.
func TestStopWinsOverAStartedAutoRetry(t *testing.T) {
	runtime := &switchRuntime{err: errors.New("readiness probe failed")}
	m, timers, id := newRetryManager(t, runtime)
	startForRetry(t, m, id, StartTarget{Mode: AutoSelect})
	failed := waitForSnapshot(t, m, id, failedWithRetry)
	block := make(chan struct{})
	runtime.setBlock(block)
	_, fire, _ := timers.last()
	fire()
	waitForSnapshot(t, m, id, func(s SnapshotResult) bool { return s.State == StateProbing && s.Generation > failed.Generation })
	if _, err := m.Stop(context.Background(), id, failed.Generation); err != nil {
		t.Fatalf("Stop with the FAILED generation during an automatic retry: %v", err)
	}
	stopped := waitForSnapshot(t, m, id, func(s SnapshotResult) bool { return s.State == StateIdle })
	if !stopped.NextRetryAt.IsZero() || stopped.VPNActive {
		t.Fatalf("snapshot after Stop = %+v", stopped)
	}
	close(block)
	time.Sleep(20 * time.Millisecond)
	if after, _ := m.Snapshot(context.Background(), id); after.State != StateIdle {
		t.Fatalf("session left IDLE after Stop: %+v", after)
	}
}

// After a retry failed again the client may still hold the first FAILED
// generation; its Stop is accepted too.
func TestStopWithAnEarlierFailedGenerationAfterARetryFailed(t *testing.T) {
	runtime := &switchRuntime{err: errors.New("readiness probe failed")}
	m, timers, id := newRetryManager(t, runtime)
	startForRetry(t, m, id, StartTarget{Mode: AutoSelect})
	first := waitForSnapshot(t, m, id, failedWithRetry)
	_, fire, _ := timers.last()
	fire()
	waitForSnapshot(t, m, id, func(s SnapshotResult) bool { return failedWithRetry(s) && s.Generation > first.Generation })
	if _, err := m.Stop(context.Background(), id, first.Generation); err != nil {
		t.Fatalf("Stop with the first FAILED generation: %v", err)
	}
	waitForSnapshot(t, m, id, func(s SnapshotResult) bool { return s.State == StateIdle })
}

// AUTO_SELECT reports PROBE_FAILED for the whole attempt; a profile that
// failed for a reason a retry cannot fix stops automatic retries.
func TestNoAutoRetryWhenAProfileFailedForAnUnfixableReason(t *testing.T) {
	runtime := &switchRuntime{err: failure(FailurePlatform, "VPN permission was revoked")}
	m, timers, id := newRetryManager(t, runtime)
	startForRetry(t, m, id, StartTarget{Mode: AutoSelect})
	snapshot := waitForSnapshot(t, m, id, func(s SnapshotResult) bool { return s.State == StateFailed })
	if snapshot.LastFailure != FailureProbe || !snapshot.NextRetryAt.IsZero() || timers.count() != 0 {
		t.Fatalf("a profile platform failure scheduled a retry: %+v, timers %d", snapshot, timers.count())
	}
}

// A name that does not resolve (also NXDOMAIN) is a resolver result a retry
// can change, so it is retried.
func TestAutoRetryAfterAResolverError(t *testing.T) {
	runtime := &switchRuntime{err: &net.DNSError{Err: "no such host", Name: "retry-test.invalid", IsNotFound: true}}
	m, _, id := newRetryManager(t, runtime)
	startForRetry(t, m, id, StartTarget{Mode: AutoSelect})
	waitForSnapshot(t, m, id, failedWithRetry)
}

// A subscription change while an automatic attempt is still connecting
// cancels that attempt, so the replaced configuration never connects.
func TestChangedSubscriptionCancelsARunningAutoRetry(t *testing.T) {
	runtime := &switchRuntime{err: errors.New("readiness probe failed")}
	m, timers, id := newRetryManager(t, runtime)
	startForRetry(t, m, id, StartTarget{Mode: AutoSelect})
	failed := waitForSnapshot(t, m, id, failedWithRetry)
	block := make(chan struct{})
	runtime.setBlock(block)
	runtime.set(nil)
	_, fire, _ := timers.last()
	fire()
	probing := waitForSnapshot(t, m, id, func(s SnapshotResult) bool { return s.State == StateProbing && s.Generation > failed.Generation })
	changed := retryTestConfig + "[[Outline]]\nServer = 'other.invalid'\nPort = 443\nPassword = 'synthetic'\n\n"
	if _, err := m.Configure(context.Background(), id, probing.Sequence, []byte(changed)); err != nil {
		t.Fatal(err)
	}
	canceled := waitForSnapshot(t, m, id, func(s SnapshotResult) bool { return s.State == StateFailed })
	if canceled.LastFailure != FailureCanceled || !canceled.NextRetryAt.IsZero() || !canceled.VPNActive {
		t.Fatalf("snapshot after the subscription change = %+v", canceled)
	}
	close(block)
	time.Sleep(20 * time.Millisecond)
	if after, _ := m.Snapshot(context.Background(), id); after.State == StateConnected {
		t.Fatal("the replaced configuration connected")
	}
}

func TestAutoRetryPausesKeepGrowingWhenAConnectionDoesNotLast(t *testing.T) {
	runtime := &healthRuntime{health: make(chan error, 1)}
	runtime.set(errors.New("readiness probe failed"))
	m, timers, id := newRetryManager(t, runtime)
	startForRetry(t, m, id, StartTarget{Mode: AutoSelect})
	waitForSnapshot(t, m, id, failedWithRetry)
	for i := 0; i < 2; i++ { // pauses 5 s and 15 s
		_, fire, _ := timers.last()
		fire()
		waitForSnapshot(t, m, id, func(s SnapshotResult) bool { return failedWithRetry(s) && timers.count() == i+2 })
	}
	runtime.set(nil)
	_, fire, _ := timers.last()
	fire()
	waitForSnapshot(t, m, id, func(s SnapshotResult) bool { return s.State == StateConnected })
	// The connection hangs before it proved healthy, and reconnecting fails.
	runtime.set(errors.New("readiness probe failed"))
	runtime.health <- errors.New("connected health check failed")
	waitForSnapshot(t, m, id, func(s SnapshotResult) bool { return failedWithRetry(s) })
	if delay, _, _ := timers.last(); delay != time.Minute {
		t.Fatalf("pause after a short connection = %s, want the schedule to continue at 60s", delay)
	}
}

// The client saw FAILED and sent Stop, but the automatic retry connected
// before the Stop arrived. The Stop for the FAILED generation still wins.
func TestStopByTheFailedGenerationAfterAnAutoRetryConnected(t *testing.T) {
	runtime := &switchRuntime{err: errors.New("readiness probe failed")}
	m, timers, id := newRetryManager(t, runtime)
	startForRetry(t, m, id, StartTarget{Mode: AutoSelect})
	failed := waitForSnapshot(t, m, id, failedWithRetry)
	runtime.set(nil)
	_, fire, _ := timers.last()
	fire()
	waitForSnapshot(t, m, id, func(s SnapshotResult) bool { return s.State == StateConnected && s.Generation > failed.Generation })
	if _, err := m.Stop(context.Background(), id, failed.Generation); err != nil {
		t.Fatalf("Stop with the FAILED generation after the automatic retry connected: %v", err)
	}
	stopped := waitForSnapshot(t, m, id, func(s SnapshotResult) bool { return s.State == StateIdle })
	if stopped.VPNActive || !stopped.NextRetryAt.IsZero() {
		t.Fatalf("snapshot after Stop = %+v", stopped)
	}
}

// secondProfileRuntime fails the first profile at once and holds the second
// until released or canceled, so an AUTO_SELECT attempt is on its second
// profile, a later generation than the attempt began with.
type secondProfileRuntime struct {
	mu      sync.Mutex
	failAll bool
	release chan struct{}
	second  chan struct{}
}

func (r *secondProfileRuntime) Start(ctx context.Context, _ SessionRef, profile RuntimeProfile) (RuntimeLease, error) {
	r.mu.Lock()
	failAll := r.failAll
	r.mu.Unlock()
	if failAll || profile.Summary.Index == 0 {
		return nil, errors.New("readiness probe failed")
	}
	select {
	case r.second <- struct{}{}:
	default:
	}
	select {
	case <-r.release:
		return noopRuntimeLease{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestChangedSubscriptionCancelsAnAutoRetryOnItsSecondProfile(t *testing.T) {
	runtime := &secondProfileRuntime{failAll: true, release: make(chan struct{}), second: make(chan struct{}, 1)}
	m, timers, id := newRetryManager(t, runtime)
	twoProfiles := retryTestConfig + "[[Outline]]\nServer = 'second.invalid'\nPort = 443\nPassword = 'synthetic'\n\n"
	configured, _ := m.Snapshot(context.Background(), id)
	if _, err := m.Configure(context.Background(), id, configured.Sequence, []byte(twoProfiles)); err != nil {
		t.Fatal(err)
	}
	startForRetry(t, m, id, StartTarget{Mode: AutoSelect})
	waitForSnapshot(t, m, id, failedWithRetry)
	runtime.mu.Lock()
	runtime.failAll = false
	runtime.mu.Unlock()
	_, fire, _ := timers.last()
	fire()
	<-runtime.second
	probing := waitForSnapshot(t, m, id, func(s SnapshotResult) bool { return s.State == StateProbing })
	changed := twoProfiles + "[[Outline]]\nServer = 'other.invalid'\nPort = 443\nPassword = 'synthetic'\n\n"
	if _, err := m.Configure(context.Background(), id, probing.Sequence, []byte(changed)); err != nil {
		t.Fatal(err)
	}
	canceled := waitForSnapshot(t, m, id, func(s SnapshotResult) bool { return s.State == StateFailed })
	if canceled.LastFailure != FailureCanceled || !canceled.NextRetryAt.IsZero() || !canceled.VPNActive {
		t.Fatalf("snapshot after the subscription change = %+v", canceled)
	}
	close(runtime.release)
	time.Sleep(20 * time.Millisecond)
	if after, _ := m.Snapshot(context.Background(), id); after.State == StateConnected {
		t.Fatal("the replaced configuration connected")
	}
}
