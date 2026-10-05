package sessionapi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"core/log"
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
	if current.State != StateConnected || current.ActiveDigest != old.Digest || current.ActiveMode != AutoSelect || current.Digest != loaded.Digest || !current.CanSwitch {
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

func TestConnectionLogCorrelationStaysOnActiveInventoryDuringLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backend.jsonl")
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	if err := log.SetPath(path); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := log.Close(); err != nil {
			t.Errorf("close test log: %v", err)
		}
	}()

	m, id, _ := newSwitchingManager(t, false)
	defer func() {
		if err := m.StopAndWait(context.Background()); err != nil {
			t.Errorf("stop test manager: %v", err)
		}
	}()
	old, err := m.Snapshot(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	correlationConfig := []byte(strings.ReplaceAll(string(replacementConfig(t)), "replacement", "log-correlation"))
	loaded, err := configureForTest(t, m, id, correlationConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.StopAndWait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var record struct {
			Category                  string `json:"category"`
			Message                   string `json:"message"`
			SessionID                 string `json:"session_id"`
			Generation                uint64 `json:"generation"`
			ConfigurationDigest       string `json:"configuration_digest"`
			LoadedConfigurationDigest string `json:"loaded_configuration_digest"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode JSON log record: %v", err)
		}
		if record.Category == "CONFIGURATION" && record.Message == "[CONFIGURATION] profile inventory loaded" && record.LoadedConfigurationDigest == loaded.Digest {
			if record.SessionID != old.SessionID || record.Generation != old.Generation || record.ConfigurationDigest != old.ActiveDigest || record.LoadedConfigurationDigest != loaded.Digest {
				t.Fatalf("load record correlation = %#v; want session=%q generation=%d active_digest=%q loaded_digest=%q", record, old.SessionID, old.Generation, old.ActiveDigest, loaded.Digest)
			}
			return
		}
	}
	t.Fatal("no profile inventory loaded event found in backend JSONL")
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
	if pending.PendingTarget == nil || pending.PendingTarget.Digest != current.Digest || pending.PendingTarget.Mode != ProfileIndex || pending.PendingTarget.Index != 0 || pending.CanSwitch || pending.PrimaryAction != "STOP" {
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

type orderedConfigLoader struct {
	entered  chan string
	releases map[string]<-chan struct{}
	configs  map[string][]byte
}

func (loader orderedConfigLoader) Load(ctx context.Context, source []byte) (LoadedConfig, error) {
	key := string(source)
	loader.entered <- key
	select {
	case <-ctx.Done():
		return LoadedConfig{}, ctx.Err()
	case <-loader.releases[key]:
		return LoadedConfig{Raw: loader.configs[key], Kind: ConfigSourceInline}, nil
	}
}

func TestNewConfigureCommitsBeforeOlderDownloadFinishes(t *testing.T) {
	m := NewManager(ManagerOptions{})
	id := configured(t, m)
	firstSource, secondSource := []byte("older-request"), []byte("newer-request")
	firstGate, secondGate := make(chan struct{}), make(chan struct{})
	var firstOnce, secondOnce sync.Once
	releaseFirst := func() { firstOnce.Do(func() { close(firstGate) }) }
	releaseSecond := func() { secondOnce.Do(func() { close(secondGate) }) }
	defer releaseFirst()
	defer releaseSecond()

	firstConfig := replacementConfig(t)
	secondConfig := []byte(strings.ReplaceAll(string(firstConfig), "replacement", "newer"))
	loader := orderedConfigLoader{
		entered: make(chan string, 2),
		releases: map[string]<-chan struct{}{
			string(firstSource): firstGate, string(secondSource): secondGate,
		},
		configs: map[string][]byte{
			string(firstSource): firstConfig, string(secondSource): secondConfig,
		},
	}
	m.loader = loader
	before, err := m.Snapshot(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := m.Configure(context.Background(), id, before.Sequence, firstSource)
		firstDone <- err
	}()
	if got := <-loader.entered; got != string(firstSource) {
		t.Fatalf("first loader source = %q", got)
	}
	go func() {
		_, err := m.Configure(context.Background(), id, before.Sequence, secondSource)
		secondDone <- err
	}()
	if got := <-loader.entered; got != string(secondSource) {
		t.Fatalf("second loader source = %q", got)
	}

	// Commit the newest request while the earlier network response is still held.
	releaseSecond()
	if err := <-secondDone; err != nil {
		t.Fatalf("newer configure: %v", err)
	}
	newest, err := m.Snapshot(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	wantDigest, err := parseConfig(secondConfig)
	if err != nil {
		t.Fatal(err)
	}
	if newest.Digest != wantDigest.digest {
		t.Fatalf("newer configure digest = %q, want %q", newest.Digest, wantDigest.digest)
	}

	releaseFirst()
	if err := <-firstDone; CodeOf(err) != FailureConflict {
		t.Fatalf("superseded older configure = %v, want conflict", err)
	}
	after, err := m.Snapshot(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Digest != newest.Digest || after.Profiles[0].Description != "newer" {
		t.Fatalf("older response overwrote accepted inventory: %#v", after)
	}
}

func TestFailedReplacementDoesNotRestorePriorConnection(t *testing.T) {
	m, id, runtime := newSwitchingManager(t, false)
	old, err := m.Snapshot(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configureForTest(t, m, id, replacementConfig(t)); err != nil {
		t.Fatal(err)
	}
	loaded, err := m.Snapshot(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(context.Background(), id, loaded.Sequence, StartTarget{
		Mode: ProfileIndex, Index: 0, Digest: loaded.Digest, ReplaceCurrent: true,
	}); err != nil {
		t.Fatal(err)
	}
	started := waitGenerationState(t, m, id, old.Generation+1, StateConnected)
	if started.ActiveDigest != loaded.Digest || started.ActiveMode != ProfileIndex || started.ActiveIndex != 0 {
		t.Fatalf("replacement did not become active: %#v", started)
	}
	select {
	case profile := <-runtime.starts:
		if profile.Summary.Description != "replacement" {
			t.Fatalf("replacement started %q", profile.Summary.Description)
		}
	case <-time.After(time.Second):
		t.Fatal("replacement runtime was not started")
	}
	runtime.failures <- errors.New("synthetic replacement health failure")
	failed := waitGenerationState(t, m, id, old.Generation+1, StateFailed)
	if failed.ActiveDigest != loaded.Digest || failed.LastFailure != FailureRuntime {
		t.Fatalf("failed replacement state = %#v", failed)
	}
	select {
	case profile := <-runtime.starts:
		t.Fatalf("old connection or fallback profile restarted after replacement failure: %#v", profile.Summary)
	default:
	}
	if err := m.StopAndWait(context.Background()); err != nil {
		t.Fatal(err)
	}
}
