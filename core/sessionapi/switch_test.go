package sessionapi

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type switchingRuntime struct {
	starts      chan RuntimeProfile
	failures    chan error
	stopEntered chan struct{}
	releaseStop chan struct{}
	cleanupErr  error
}

func (r *switchingRuntime) Start(_ context.Context, _ SessionRef, p RuntimeProfile) (RuntimeLease, error) {
	r.starts <- p
	return switchingLease{r}, nil
}

type switchingLease struct{ r *switchingRuntime }

func (l switchingLease) HealthFailures() <-chan error { return l.r.failures }
func (l switchingLease) Stop(context.Context) error {
	if l.r.stopEntered != nil {
		l.r.stopEntered <- struct{}{}
	}
	if l.r.releaseStop != nil {
		<-l.r.releaseStop
	}
	return l.r.cleanupErr
}
func newSwitchingManager(t *testing.T, blocked bool) (*Manager, string, *switchingRuntime) {
	t.Helper()
	r := &switchingRuntime{starts: make(chan RuntimeProfile, 8), failures: make(chan error, 1)}
	if blocked {
		r.stopEntered = make(chan struct{}, 8)
		r.releaseStop = make(chan struct{})
	}
	m := NewManager(ManagerOptions{Runtime: r})
	id := configured(t, m)
	if _, err := startForTest(t, m, id, StartTarget{Mode: AutoSelect}); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, id, StateConnected)
	<-r.starts
	return m, id, r
}
func replacementConfig(t *testing.T) []byte {
	return []byte(strings.ReplaceAll(string(fixture(t)), "outline-first", "replacement"))
}
func TestLoadedInventoryDoesNotChangeActiveRecovery(t *testing.T) {
	m, id, r := newSwitchingManager(t, false)
	old, _ := m.Snapshot(context.Background(), id)
	loaded, err := configureForTest(t, m, id, replacementConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	current, _ := m.Snapshot(context.Background(), id)
	if current.State != StateConnected || current.ActiveDigest != old.Digest || current.Digest != loaded.Digest || !current.CanSwitch {
		t.Fatalf("load changed active session: %#v", current)
	}
	r.failures <- errors.New("synthetic outage")
	recovered := waitGenerationState(t, m, id, old.Generation+1, StateConnected)
	started := <-r.starts
	if started.Summary.Description != "outline-first" || recovered.ActiveDigest != old.Digest || recovered.Digest != loaded.Digest {
		t.Fatalf("recovery used loaded inventory: %#v %#v", started, recovered)
	}
	if _, err := configureForTest(t, m, id, []byte("invalid")); err == nil {
		t.Fatal("invalid configuration accepted")
	}
	after, _ := m.Snapshot(context.Background(), id)
	if after.Digest != loaded.Digest || after.State != StateConnected {
		t.Fatalf("failed load disrupted connection: %#v", after)
	}
	if err := m.StopAndWait(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestSwitchWaitsForCleanupAndCapturesInventory(t *testing.T) {
	m, id, r := newSwitchingManager(t, true)
	old, _ := m.Snapshot(context.Background(), id)
	if _, err := configureForTest(t, m, id, replacementConfig(t)); err != nil {
		t.Fatal(err)
	}
	current, _ := m.Snapshot(context.Background(), id)
	if _, err := m.Start(context.Background(), id, current.Sequence, StartTarget{Mode: ProfileIndex, Index: 0, Digest: old.Digest, ReplaceCurrent: true}); CodeOf(err) != FailureConflict {
		t.Fatalf("stale inventory accepted: %v", err)
	}
	if _, err := m.Start(context.Background(), id, current.Sequence, StartTarget{Mode: ProfileIndex, Index: 99, ReplaceCurrent: true}); CodeOf(err) != FailureInvalidArgument {
		t.Fatalf("invalid target: %v", err)
	}
	if _, err := m.Start(context.Background(), id, current.Sequence, StartTarget{Mode: ProfileIndex, Index: 0}); CodeOf(err) != FailureConflict {
		t.Fatalf("implicit replacement: %v", err)
	}
	if _, err := m.Start(context.Background(), id, current.Sequence, StartTarget{Mode: ProfileIndex, Index: 0, Digest: current.Digest, ReplaceCurrent: true}); err != nil {
		t.Fatal(err)
	}
	<-r.stopEntered
	pending, _ := m.Snapshot(context.Background(), id)
	if pending.PendingTarget == nil || pending.PendingTarget.Digest != current.Digest || pending.CanSwitch || pending.PrimaryAction != "STOP" {
		t.Fatalf("pending state: %#v", pending)
	}
	if _, err := startForTest(t, m, id, StartTarget{Mode: AutoSelect, ReplaceCurrent: true}); CodeOf(err) != FailureConflict {
		t.Fatalf("competing switch accepted: %v", err)
	}
	if _, err := configureForTest(t, m, id, fixture(t)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.starts:
		t.Fatal("replacement started before cleanup")
	default:
	}
	close(r.releaseStop)
	switched := waitGenerationState(t, m, id, old.Generation+1, StateConnected)
	started := <-r.starts
	if started.Summary.Description != "replacement" || switched.ActiveDigest != current.Digest || switched.ActiveMode != ProfileIndex {
		t.Fatalf("switch target changed: %#v %#v", started, switched)
	}
	if err := m.StopAndWait(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestPendingSwitchCanceledByStopOrShutdown(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop", true: "shutdown"}[shutdown], func(t *testing.T) {
			m, id, r := newSwitchingManager(t, true)
			old, _ := m.Snapshot(context.Background(), id)
			if _, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex, Index: 1, ReplaceCurrent: true}); err != nil {
				t.Fatal(err)
			}
			<-r.stopEntered
			done := make(chan error, 1)
			if shutdown {
				go func() { done <- m.StopAndWait(context.Background()) }()
				deadline := time.Now().Add(time.Second)
				for {
					m.session.mu.Lock()
					closing := m.session.closing
					m.session.mu.Unlock()
					if closing {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("shutdown did not fence session")
					}
					time.Sleep(time.Millisecond)
				}
			} else if _, err := m.Stop(context.Background(), id, old.Generation); err != nil {
				t.Fatal(err)
			}
			close(r.releaseStop)
			if shutdown {
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			}
			idle := waitState(t, m, id, StateIdle)
			m.startFailover(m.session, old.Generation)
			after, _ := m.Snapshot(context.Background(), id)
			if idle.PendingTarget != nil || after.Generation != old.Generation {
				t.Fatalf("canceled switch restarted: %#v", after)
			}
		})
	}
}
func TestSwitchCleanupFailureBlocksReplacement(t *testing.T) {
	m, id, r := newSwitchingManager(t, true)
	r.cleanupErr = errors.New("synthetic cleanup failure")
	if _, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex, Index: 1, ReplaceCurrent: true}); err != nil {
		t.Fatal(err)
	}
	<-r.stopEntered
	close(r.releaseStop)
	failed := waitState(t, m, id, StateFailed)
	if failed.LastFailure != FailureCleanup || failed.PendingTarget != nil || failed.CanSwitch {
		t.Fatalf("cleanup failure: %#v", failed)
	}
	select {
	case <-r.starts:
		t.Fatal("replacement ran after cleanup failure")
	default:
	}
}

type gatedConfigLoader struct {
	entered chan struct{}
	release chan struct{}
	raw     []byte
}

func (l gatedConfigLoader) Load(_ context.Context, _ []byte) (LoadedConfig, error) {
	l.entered <- struct{}{}
	<-l.release
	return LoadedConfig{Raw: l.raw, Kind: ConfigSourceInline}, nil
}
func TestConfigureAcceptsConnectionTransitionsDuringDownload(t *testing.T) {
	m, id, _ := newSwitchingManager(t, false)
	l := gatedConfigLoader{make(chan struct{}, 1), make(chan struct{}), replacementConfig(t)}
	m.loader = l
	old, _ := m.Snapshot(context.Background(), id)
	done := make(chan error, 1)
	go func() {
		_, err := m.Configure(context.Background(), id, old.Sequence, []byte("https://example.invalid"))
		done <- err
	}()
	<-l.entered
	if _, err := m.Stop(context.Background(), id, old.Generation); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, id, StateIdle)
	close(l.release)
	if err := <-done; err != nil {
		t.Fatalf("state transition rejected load: %v", err)
	}
	current, _ := m.Snapshot(context.Background(), id)
	if current.State != StateConfigured || current.Profiles[0].Description != "replacement" {
		t.Fatalf("download not accepted: %#v", current)
	}
}

func TestNewConfigureSupersedesBlockedDownload(t *testing.T) {
	m := NewManager(ManagerOptions{})
	id := configured(t, m)
	l := gatedConfigLoader{make(chan struct{}, 2), make(chan struct{}), replacementConfig(t)}
	m.loader = l
	current, _ := m.Snapshot(context.Background(), id)
	first, second := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := m.Configure(context.Background(), id, current.Sequence, []byte("first"))
		first <- err
	}()
	<-l.entered
	go func() {
		_, err := m.Configure(context.Background(), id, current.Sequence, []byte("second"))
		second <- err
	}()
	<-l.entered
	close(l.release)
	if err := <-first; CodeOf(err) != FailureConflict {
		t.Fatalf("superseded load: %v", err)
	}
	if err := <-second; err != nil {
		t.Fatalf("newest load: %v", err)
	}
}
