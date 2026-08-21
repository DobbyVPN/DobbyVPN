package v2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/redacted_mixed.toml")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestConfigurePreservesMixedSourceOrderAndTelemetryIsLocalOnly(t *testing.T) {
	m := NewManager(ManagerOptions{})
	id, err := m.CreateSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.Configure(context.Background(), id, "configure-1", fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Profiles) != 4 {
		t.Fatalf("profiles = %#v", got.Profiles)
	}
	want := []Protocol{ProtocolOutline, ProtocolXray, ProtocolTrustTunnel, ProtocolOutline}
	for i := range want {
		if got.Profiles[i].Index != i || got.Profiles[i].Protocol != want[i] {
			t.Fatalf("profile %d = %#v", i, got.Profiles[i])
		}
	}
	if len(got.Warnings) != 1 || got.Warnings[0].Code != "TELEMETRY_IGNORED" {
		t.Fatalf("warnings = %#v", got.Warnings)
	}
	if got.Digest == "" {
		t.Fatal("empty digest")
	}
	again, err := m.Configure(context.Background(), id, "configure-1", []byte("not TOML"))
	if err != nil || again.Digest != got.Digest {
		t.Fatalf("idempotent configure = %#v, %v", again, err)
	}
	if _, err := m.Configure(context.Background(), id, "configure-2", []byte("[Outline]\nServer='x'")); CodeOf(err) != FailureMalformedConfig {
		t.Fatalf("bad config error = %v", err)
	}
}

func TestConfigureRejectsRemovedCloakProfiles(t *testing.T) {
	raw := strings.Join([]string{
		"[[Outline]]", `Description = "supported-before"`, `Server = "198.51.100.20"`, "Port = 443", `Password = "redacted"`,
		"", "[[Xray]]", `Description = "legacy-cloak"`, "Cloak = true", `Server = "cloak.invalid"`, `Password = "do-not-return"`,
		"", "[[TrustTunnel]]", `Description = "supported-after"`, `vpn_mode = "general"`, "|endpoint|", `hostname = "vpn.invalid"`, `addresses = ["198.51.100.21:443"]`, `username = "redacted"`, `password = "redacted"`, "|socks|", `address = "127.0.0.1:10808"`,
	}, "\n")
	m := NewManager(ManagerOptions{})
	id, err := m.CreateSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Configure(context.Background(), id, "cloak-removed", []byte(raw))
	if CodeOf(err) != FailureUnsupported || err.Error() != "UNSUPPORTED: configuration contains a removed Cloak profile" {
		t.Fatalf("removed Cloak result = %v", err)
	}
	if strings.Contains(err.Error(), "cloak.invalid") || strings.Contains(err.Error(), "do-not-return") {
		t.Fatalf("removed Cloak error leaked source: %v", err)
	}
}

func TestSubscribeReplaysAndPublishesOrderedEvents(t *testing.T) {
	m := NewManager(ManagerOptions{})
	id, err := m.CreateSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	events, closeSubscription, err := m.Subscribe(context.Background(), id, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSubscription()
	if _, err := m.Configure(context.Background(), id, "configure", fixture(t)); err != nil {
		t.Fatal(err)
	}
	lastSequence := uint64(0)
	configuredSeen := false
	deadline := time.After(time.Second)
	for !configuredSeen {
		select {
		case event := <-events:
			if event.SessionID != id || event.Sequence <= lastSequence {
				t.Fatalf("unordered configured event = %#v", event)
			}
			lastSequence = event.Sequence
			configuredSeen = event.State == StateConfigured
		case <-deadline:
			t.Fatal("timed out waiting for configured event")
		}
	}
	if err := m.DestroySession(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	destroyedSeen := false
	deadline = time.After(time.Second)
	for !destroyedSeen {
		select {
		case event, ok := <-events:
			if !ok || event.Sequence <= lastSequence {
				t.Fatalf("destroy event = %#v, open=%v", event, ok)
			}
			lastSequence = event.Sequence
			destroyedSeen = event.State == StateDestroyed
		case <-deadline:
			t.Fatal("timed out waiting for destroyed event")
		}
	}
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("subscription remained open after destroy")
		}
	case <-time.After(time.Second):
		t.Fatal("subscription did not close after destroy")
	}
}

func TestCreateSessionConflictsWhileAnotherGenerationIsRecoverable(t *testing.T) {
	m := NewManager(ManagerOptions{Runtime: &fakeRuntime{latency: map[int]int64{0: 1}}, Platform: &fakePlatform{}})
	first := configured(t, m)
	if _, err := m.Start(context.Background(), first, "start", StartTarget{Mode: AutoSelect}); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, first, StateConnected)
	if _, err := m.CreateSession(context.Background()); CodeOf(err) != FailureConflict {
		t.Fatalf("CreateSession while connected = %v", err)
	}
	if recovered, err := m.RecoverActiveSession(context.Background()); err != nil || recovered != first {
		t.Fatalf("recovery = %q, %v", recovered, err)
	}
}

func TestNormalizationAndPublicResultsNeverExposeFixtureSecrets(t *testing.T) {
	m := NewManager(ManagerOptions{})
	id := configured(t, m)
	s, err := m.get(id)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	profiles := append([]RuntimeProfile(nil), s.profiles...)
	s.mu.Unlock()
	if profiles[0].NormalizedFormat != ConfigTransportURL || !strings.HasPrefix(string(profiles[0].NormalizedConfig), "ss://") {
		t.Fatalf("outline normalization = %#v", profiles[0])
	}
	if profiles[1].NormalizedFormat != ConfigJSON || !json.Valid(profiles[1].NormalizedConfig) {
		t.Fatalf("xray normalization = %q", profiles[1].NormalizedConfig)
	}
	if profiles[2].NormalizedFormat != ConfigTOML || !strings.Contains(string(profiles[2].NormalizedConfig), "[endpoint]") {
		t.Fatalf("trusttunnel normalization = %q", profiles[2].NormalizedConfig)
	}
	if len(profiles[0].ExcludeCIDRs) != 1 || profiles[0].ExcludeCIDRs[0] != "203.0.113.0/24" {
		t.Fatalf("routing inputs = %#v", profiles[0].ExcludeCIDRs)
	}
	if len(profiles[2].PreflightHosts) != 1 || profiles[2].PreflightHosts[0] != "vpn.invalid" {
		t.Fatalf("DNS preflight inputs = %#v", profiles[2].PreflightHosts)
	}
	result, err := m.Configure(context.Background(), id, "bad", []byte("not = [valid"))
	if err == nil || result.Digest != "" || CodeOf(err) != FailureMalformedConfig {
		t.Fatalf("malformed result=%#v err=%v", result, err)
	}
	events, err := m.Observe(context.Background(), id, 0)
	if err != nil {
		t.Fatal(err)
	}
	public := fmt.Sprintf("%#v %#v %v", result, events.Events, err)
	for _, secret := range []string{"redacted", "198.51.100.10", "telemetry.invalid", "ApiToken", "Password"} {
		if strings.Contains(public, secret) {
			t.Fatalf("public response leaked %q: %s", secret, public)
		}
	}
}

func TestConfigureRejectsProfilesRejectedByLegacyInterpreter(t *testing.T) {
	for name, raw := range map[string]string{
		"outline password": "[[Outline]]\nServer='vpn.invalid'\nPort=443\n",
		"outline server":   "[[Outline]]\nPassword='secret'\nPort=443\n",
		"outline port":     "[[Outline]]\nServer='vpn.invalid'\nPassword='secret'\n",
		"xray outbounds":   "[[Xray]]\nDescription='empty'\n",
	} {
		t.Run(name, func(t *testing.T) {
			m := NewManager(ManagerOptions{})
			id, err := m.CreateSession(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.Configure(context.Background(), id, "configure", []byte(raw)); CodeOf(err) != FailureMalformedConfig {
				t.Fatalf("Configure error = %v", err)
			}
		})
	}
}

func TestSlowPlatformPublicationPreservesEventOrder(t *testing.T) {
	p := &orderedPlatform{firstEntered: make(chan struct{}), releaseFirst: make(chan struct{})}
	m := NewManager(ManagerOptions{Platform: p})
	id, err := m.CreateSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Configure(context.Background(), id, "configure", fixture(t)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.firstEntered:
	case <-time.After(time.Second):
		t.Fatal("first publication did not begin")
	}
	if _, err := m.Start(context.Background(), id, "start", StartTarget{Mode: ProfileIndex, Index: 0}); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, id, StateFailed) // default runtime queues the final failure event
	close(p.releaseFirst)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		n := len(p.events)
		p.mu.Unlock()
		if n >= 5 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	p.mu.Lock()
	events := append([]Event(nil), p.events...)
	p.mu.Unlock()
	if len(events) < 5 {
		t.Fatalf("published events=%#v", events)
	}
	for i, event := range events {
		if event.Sequence != uint64(i+1) {
			t.Fatalf("event publication reordered at %d: %#v", i, event)
		}
	}
}

func TestSlowPlatformPublicationCannotBlockTheSessionMutex(t *testing.T) {
	p := &orderedPlatform{firstEntered: make(chan struct{}), releaseFirst: make(chan struct{})}
	m := NewManager(ManagerOptions{Platform: p})
	id, err := m.CreateSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.get(id)
	if err != nil {
		t.Fatal(err)
	}
	appended := make(chan struct{})
	go func() {
		s.mu.Lock()
		for i := 0; i < 256; i++ {
			m.appendLocked(s, Event{State: StateIdle})
		}
		s.mu.Unlock()
		close(appended)
	}()
	select {
	case <-p.firstEntered:
	case <-time.After(time.Second):
		t.Fatal("first publication did not begin")
	}
	select {
	case <-appended:
	case <-time.After(time.Second):
		t.Fatal("slow platform publication blocked the session mutex")
	}
	close(p.releaseFirst)
	if err := m.DestroySession(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		count := len(p.events)
		p.mu.Unlock()
		if count >= 257 { // 256 synthetic events plus Destroyed.
			return
		}
		time.Sleep(time.Millisecond)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	t.Fatalf("slow publisher dropped queued events: got %d", len(p.events))
}

func TestDestroyDoesNotRaceACommandThatAlreadyResolvedSession(t *testing.T) {
	raw := fixture(t)
	for i := 0; i < 50; i++ {
		m := NewManager(ManagerOptions{})
		id, err := m.CreateSession(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); <-start; _, _ = m.Configure(context.Background(), id, "configure", raw) }()
		go func() { defer wg.Done(); <-start; _ = m.DestroySession(context.Background(), id) }()
		close(start)
		wg.Wait()
	}
}

func TestAutoSelectionUsesLatencyThenSourceOrderAndEventsAreMonotonic(t *testing.T) {
	r := &fakeRuntime{latency: map[int]int64{0: 50, 1: 10, 2: 10, 3: 60}}
	m := NewManager(ManagerOptions{Runtime: r, Platform: &fakePlatform{}})
	id := configured(t, m)
	start, err := m.Start(context.Background(), id, "start-1", StartTarget{Mode: AutoSelect})
	if err != nil {
		t.Fatal(err)
	}
	if again, duplicateErr := m.Start(context.Background(), id, "start-1", StartTarget{Mode: AutoSelect}); duplicateErr != nil || again != start {
		t.Fatalf("idempotent start = %#v %v", again, duplicateErr)
	}
	s := waitState(t, m, id, StateConnected)
	if s.ActiveProfile == nil || s.ActiveProfile.Index != 1 {
		t.Fatalf("active = %#v", s.ActiveProfile)
	}
	if _, stopErr := m.Stop(context.Background(), id, "wrong-generation", start.Generation+1); CodeOf(stopErr) != FailureStaleGeneration {
		t.Fatalf("stale stop = %v", stopErr)
	}
	events, err := m.Observe(context.Background(), id, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range events.Events {
		if e.Sequence != uint64(i+1) || e.SessionID != id {
			t.Fatalf("event %d = %#v", i, e)
		}
	}
	probeCount := 0
	for _, e := range events.Events {
		if e.State == StateProbing && e.Profile != nil {
			probeCount++
		}
	}
	if probeCount != 4 {
		t.Fatalf("probe event count=%d events=%#v", probeCount, events.Events)
	}
}

func TestStopReportsCleanupFailureAndBlocksRestart(t *testing.T) {
	runtime := &cleanupErrorRuntime{err: errors.New("cleanup failed")}
	m := NewManager(ManagerOptions{Runtime: runtime, Platform: &fakePlatform{}})
	id := configured(t, m)
	start, err := m.Start(context.Background(), id, "start", StartTarget{Mode: ProfileIndex, Index: 0})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, id, StateConnected)
	if _, err := m.Stop(context.Background(), id, "stop", start.Generation); err != nil {
		t.Fatal(err)
	}
	snapshot := waitState(t, m, id, StateFailed)
	if !snapshot.CleanupComplete || snapshot.LastFailure != FailureCleanup {
		t.Fatalf("cleanup snapshot=%#v", snapshot)
	}
	if _, err := m.Start(context.Background(), id, "restart", StartTarget{Mode: ProfileIndex, Index: 0}); CodeOf(err) != FailureConflict {
		t.Fatalf("restart error=%v, want %s", err, FailureConflict)
	}
	if _, err := m.Configure(context.Background(), id, "reconfigure", fixture(t)); CodeOf(err) != FailureConflict {
		t.Fatalf("configure after cleanup failure error=%v, want %s", err, FailureConflict)
	}
	if err := m.DestroySession(context.Background(), id); CodeOf(err) != FailureConflict {
		t.Fatalf("destroy after cleanup failure error=%v, want %s", err, FailureConflict)
	}
}

func TestStopAcknowledgesAlreadyCleanedTerminalGeneration(t *testing.T) {
	runtime := &monitoringRuntime{failures: make(chan struct{}, 1), stopped: make(chan uint64, 2)}
	platform := &eventPlatform{events: make(chan Event, 32)}
	m := NewManager(ManagerOptions{Runtime: runtime, Platform: platform})
	id := configured(t, m)
	start, err := m.Start(context.Background(), id, "start", StartTarget{Mode: ProfileIndex, Index: 0})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, id, StateConnected)
	if _, err := m.ReportHealth(context.Background(), id, start.Generation, false); err != nil {
		t.Fatal(err)
	}
	snapshot := waitState(t, m, id, StateFailed)
	if !snapshot.CleanupComplete || snapshot.LastFailure != FailureRuntime {
		t.Fatalf("health-failure snapshot=%#v", snapshot)
	}
	if _, err := m.Stop(context.Background(), id, "stop-after-health-failure", start.Generation); err != nil {
		t.Fatalf("already-cleaned terminal stop: %v", err)
	}
	if _, err := m.Stop(context.Background(), id, "stop-after-health-failure", start.Generation); err != nil {
		t.Fatalf("repeated already-cleaned terminal stop: %v", err)
	}
}

func TestPlatformAcquisitionErrorStillOwnsAndReportsReturnedLeaseCleanup(t *testing.T) {
	want := errors.New("platform rollback failed")
	m := NewManager(ManagerOptions{
		Runtime:  &fakeRuntime{latency: map[int]int64{0: 1}},
		Platform: errorWithLeasePlatform{prepareErr: errors.New("prepare failed"), releaseErr: want},
	})
	id := configured(t, m)
	if _, err := m.Start(context.Background(), id, "start", StartTarget{Mode: ProfileIndex, Index: 0}); err != nil {
		t.Fatal(err)
	}
	snapshot := waitState(t, m, id, StateFailed)
	if !snapshot.CleanupComplete || snapshot.LastFailure != FailureCleanup {
		t.Fatalf("cleanup snapshot=%#v", snapshot)
	}
}

func TestRuntimeAcquisitionErrorStillOwnsAndReportsReturnedLeaseCleanup(t *testing.T) {
	want := errors.New("runtime rollback failed")
	m := NewManager(ManagerOptions{
		Runtime:  errorWithLeaseRuntime{startErr: errors.New("start failed"), stopErr: want},
		Platform: &fakePlatform{},
	})
	id := configured(t, m)
	if _, err := m.Start(context.Background(), id, "start", StartTarget{Mode: ProfileIndex, Index: 0}); err != nil {
		t.Fatal(err)
	}
	snapshot := waitState(t, m, id, StateFailed)
	if !snapshot.CleanupComplete || snapshot.LastFailure != FailureCleanup {
		t.Fatalf("cleanup snapshot=%#v", snapshot)
	}
}

func TestAutoSelectionProbesWithFreshPlatformLeaseBeforeEachRuntimeProbe(t *testing.T) {
	order := &recordedOrder{}
	runtime := &orderedProbeRuntime{order: order, latency: map[int]int64{0: 40, 1: 10, 2: 30, 3: 20}}
	platform := &orderedProbePlatform{order: order, events: make(chan Event, 32)}
	m := NewManager(ManagerOptions{Runtime: runtime, Platform: platform})
	id := configured(t, m)
	if _, err := m.Start(context.Background(), id, "start", StartTarget{Mode: AutoSelect}); err != nil {
		t.Fatal(err)
	}
	waitForEvent(t, platform.events, 1, StateConnected)
	wantPrefix := []string{
		"prepare-1", "probe-0", "release-1",
		"prepare-2", "probe-1", "release-2",
		"prepare-3", "probe-2", "release-3",
		"prepare-4", "probe-3", "release-4",
		"prepare-5", "start-1",
	}
	if got := order.itemsCopy(); !sameStrings(got, wantPrefix) {
		t.Fatalf("probe ordering=%v, want=%v", got, wantPrefix)
	}
}

func TestAutoSelectionReleasesProbeLeaseWhenCanceled(t *testing.T) {
	entered := make(chan struct{}, 1)
	released := make(chan struct{}, 1)
	runtime := &fakeRuntime{latency: map[int]int64{0: 1}, blockProbe: make(chan struct{}), probeEntered: entered}
	platform := &releaseSignalPlatform{released: released}
	m := NewManager(ManagerOptions{Runtime: runtime, Platform: platform})
	id := configured(t, m)
	start, err := m.Start(context.Background(), id, "start", StartTarget{Mode: AutoSelect})
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	if _, err := m.Stop(context.Background(), id, "stop", start.Generation); err != nil {
		t.Fatal(err)
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("canceled probe did not release its platform lease")
	}
	waitState(t, m, id, StateIdle)
}

func TestAutoSelectionReportsPlatformProbePreparationFailure(t *testing.T) {
	platform := failingProbePlatform{events: make(chan Event, 8)}
	m := NewManager(ManagerOptions{Runtime: &fakeRuntime{latency: map[int]int64{0: 1}}, Platform: platform})
	id := configured(t, m)
	if _, err := m.Start(context.Background(), id, "start", StartTarget{Mode: AutoSelect}); err != nil {
		t.Fatal(err)
	}
	event := waitForEvent(t, platform.events, 1, StateFailed)
	if event.Failure != FailurePlatform {
		t.Fatalf("failure=%s, want %s", event.Failure, FailurePlatform)
	}
}

func TestStopDuringProbePreventsLateConnectedAndAllowsRestartAfterCleanup(t *testing.T) {
	r := &fakeRuntime{latency: map[int]int64{0: 1}, blockProbe: make(chan struct{}), probeEntered: make(chan struct{}, 1)}
	p := &fakePlatform{}
	m := NewManager(ManagerOptions{Runtime: r, Platform: p})
	id := configured(t, m)
	first, err := m.Start(context.Background(), id, "start-1", StartTarget{Mode: AutoSelect})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.probeEntered:
	case <-time.After(time.Second):
		t.Fatal("probe did not start")
	}
	if _, stopErr := m.Stop(context.Background(), id, "stop-1", first.Generation); stopErr != nil {
		t.Fatal(stopErr)
	}
	waitState(t, m, id, StateIdle)
	close(r.blockProbe)
	time.Sleep(10 * time.Millisecond) // permits the deliberately stale probe completion to return
	if got, _ := m.Snapshot(context.Background(), id); got.State == StateConnected {
		t.Fatalf("stale completion connected: %#v", got)
	}
	second, err := m.Start(context.Background(), id, "start-2", StartTarget{Mode: ProfileIndex, Index: 0})
	if err != nil {
		t.Fatal(err)
	}
	if second.Generation != first.Generation+1 {
		t.Fatalf("generations %d %d", first.Generation, second.Generation)
	}
	waitState(t, m, id, StateConnected)
}

func TestCleanupIsLIFOAndRunsBeforeRestart(t *testing.T) {
	order := make([]string, 0, 2)
	var orderMu sync.Mutex
	r := &fakeRuntime{latency: map[int]int64{0: 1}, stopHook: func() { orderMu.Lock(); order = append(order, "runtime"); orderMu.Unlock() }}
	p := &fakePlatform{releaseHook: func() { orderMu.Lock(); order = append(order, "platform"); orderMu.Unlock() }}
	m := NewManager(ManagerOptions{Runtime: r, Platform: p})
	id := configured(t, m)
	first, err := m.Start(context.Background(), id, "start-1", StartTarget{Mode: ProfileIndex, Index: 0})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, id, StateConnected)
	if _, err := m.Stop(context.Background(), id, "stop-1", first.Generation); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, id, StateIdle)
	orderMu.Lock()
	got := append([]string(nil), order...)
	orderMu.Unlock()
	if len(got) != 2 || got[0] != "runtime" || got[1] != "platform" {
		t.Fatalf("cleanup order = %#v", got)
	}
	if _, err := m.Start(context.Background(), id, "start-2", StartTarget{Mode: ProfileIndex, Index: 0}); err != nil {
		t.Fatalf("restart after cleanup: %v", err)
	}
}

func TestRuntimeOwnedHealthFailureCleansUpBeforeAutoFailover(t *testing.T) {
	failures := make(chan struct{}, 1)
	defer close(failures)
	runtime := &monitoringRuntime{failures: failures, stopped: make(chan uint64, 2)}
	platform := &eventPlatform{events: make(chan Event, 32)}
	m := NewManager(ManagerOptions{Runtime: runtime, Platform: platform})
	id, err := m.CreateSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	profile := RuntimeProfile{
		Summary:          ProfileSummary{Protocol: ProtocolOutline},
		NormalizedFormat: ConfigTransportURL,
		NormalizedConfig: []byte("ss://normalized"),
	}
	if _, configureErr := m.ConfigureNormalized(context.Background(), id, "configure", profile); configureErr != nil {
		t.Fatal(configureErr)
	}
	first, err := m.Start(context.Background(), id, "start", StartTarget{Mode: AutoSelect})
	if err != nil {
		t.Fatal(err)
	}
	waitForEvent(t, platform.events, first.Generation, StateConnected)
	failures <- struct{}{}
	waitForEvent(t, platform.events, first.Generation, StateStopping)
	waitForEvent(t, platform.events, first.Generation, StateIdle)
	if stopped := <-runtime.stopped; stopped != first.Generation {
		t.Fatalf("cleanup stopped generation %d, want %d", stopped, first.Generation)
	}
	second := waitForEvent(t, platform.events, first.Generation+1, StateProbing)
	if second.Generation != first.Generation+1 {
		t.Fatalf("failover generation = %d, want %d", second.Generation, first.Generation+1)
	}
	waitForEvent(t, platform.events, first.Generation+1, StateConnected)
}

func TestRuntimeOwnedHealthFailureDoesNotReplaceExplicitProfile(t *testing.T) {
	failures := make(chan struct{}, 1)
	defer close(failures)
	runtime := &monitoringRuntime{failures: failures, stopped: make(chan uint64, 2)}
	platform := &eventPlatform{events: make(chan Event, 32)}
	m := NewManager(ManagerOptions{Runtime: runtime, Platform: platform})
	id, err := m.CreateSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	profile := RuntimeProfile{
		Summary:          ProfileSummary{Protocol: ProtocolOutline},
		NormalizedFormat: ConfigTransportURL,
		NormalizedConfig: []byte("ss://normalized"),
	}
	if _, configureErr := m.ConfigureNormalized(context.Background(), id, "configure", profile); configureErr != nil {
		t.Fatal(configureErr)
	}
	first, err := m.Start(context.Background(), id, "start", StartTarget{Mode: ProfileIndex, Index: 0})
	if err != nil {
		t.Fatal(err)
	}
	waitForEvent(t, platform.events, first.Generation, StateConnected)
	failures <- struct{}{}
	waitForEvent(t, platform.events, first.Generation, StateStopping)
	failed := waitForEvent(t, platform.events, first.Generation, StateFailed)
	if failed.Failure != FailureRuntime {
		t.Fatalf("failure=%s, want %s", failed.Failure, FailureRuntime)
	}
	if stopped := <-runtime.stopped; stopped != first.Generation {
		t.Fatalf("cleanup stopped generation %d, want %d", stopped, first.Generation)
	}
	snapshot, err := m.Snapshot(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Generation != first.Generation || snapshot.State != StateFailed || snapshot.LastFailure != FailureRuntime || !snapshot.CleanupComplete {
		t.Fatalf("explicit-profile health failure snapshot=%#v", snapshot)
	}
	select {
	case event := <-platform.events:
		if event.Generation > first.Generation {
			t.Fatalf("explicit profile was replaced by generation %d", event.Generation)
		}
	case <-time.After(25 * time.Millisecond):
	}
}

func TestStopWaitsForNonCooperativeStartBeforeCleanupAndRestart(t *testing.T) {
	entered := make(chan struct{})
	releaseStart := make(chan struct{})
	stopped := make(chan struct{}, 1)
	r := blockingStartRuntime{entered: entered, release: releaseStart, stopped: stopped}
	m := NewManager(ManagerOptions{Runtime: r, Platform: &fakePlatform{}})
	id := configured(t, m)
	start, err := m.Start(context.Background(), id, "start-1", StartTarget{Mode: ProfileIndex, Index: 0})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("runtime start did not begin")
	}
	if _, err := m.Stop(context.Background(), id, "stop-1", start.Generation); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Snapshot(context.Background(), id); got.State != StateStopping || got.CleanupComplete {
		t.Fatalf("stop released early: %#v", got)
	}
	if _, err := m.Start(context.Background(), id, "start-2", StartTarget{Mode: ProfileIndex, Index: 0}); CodeOf(err) != FailureConflict {
		t.Fatalf("restart while start blocked = %v", err)
	}
	close(releaseStart)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("late runtime lease was not stopped")
	}
	waitState(t, m, id, StateIdle)
	if _, err := m.Start(context.Background(), id, "start-3", StartTarget{Mode: ProfileIndex, Index: 0}); err != nil {
		t.Fatalf("restart after worker completed: %v", err)
	}
}

func TestStopReportsLateRuntimeLeaseCleanupFailure(t *testing.T) {
	entered := make(chan struct{})
	releaseStart := make(chan struct{})
	stopped := make(chan struct{}, 1)
	r := blockingStartRuntime{
		entered: entered,
		release: releaseStart,
		stopped: stopped,
		err:     errors.New("late runtime cleanup failed"),
	}
	m := NewManager(ManagerOptions{Runtime: r, Platform: &fakePlatform{}})
	id := configured(t, m)
	start, err := m.Start(context.Background(), id, "start", StartTarget{Mode: ProfileIndex, Index: 0})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("runtime start did not begin")
	}
	if _, err := m.Stop(context.Background(), id, "stop", start.Generation); err != nil {
		t.Fatal(err)
	}
	close(releaseStart)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("late runtime lease was not stopped")
	}
	snapshot := waitState(t, m, id, StateFailed)
	if !snapshot.CleanupComplete || snapshot.LastFailure != FailureCleanup {
		t.Fatalf("cleanup snapshot=%#v", snapshot)
	}
}

func TestStopReportsLatePlatformLeaseCleanupFailure(t *testing.T) {
	entered := make(chan struct{})
	releasePrepare := make(chan struct{})
	released := make(chan struct{}, 1)
	platform := blockingPreparePlatform{
		entered:  entered,
		release:  releasePrepare,
		released: released,
		err:      errors.New("late platform cleanup failed"),
	}
	m := NewManager(ManagerOptions{Runtime: &fakeRuntime{latency: map[int]int64{0: 1}}, Platform: platform})
	id := configured(t, m)
	start, err := m.Start(context.Background(), id, "start", StartTarget{Mode: ProfileIndex, Index: 0})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("platform preparation did not begin")
	}
	if _, err := m.Stop(context.Background(), id, "stop", start.Generation); err != nil {
		t.Fatal(err)
	}
	close(releasePrepare)
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("late platform lease was not released")
	}
	snapshot := waitState(t, m, id, StateFailed)
	if !snapshot.CleanupComplete || snapshot.LastFailure != FailureCleanup {
		t.Fatalf("cleanup snapshot=%#v", snapshot)
	}
}

func TestDefaultRuntimeFailsTypedUnsupported(t *testing.T) {
	m := NewManager(ManagerOptions{})
	id := configured(t, m)
	if _, err := m.Start(context.Background(), id, "start", StartTarget{Mode: ProfileIndex, Index: 0}); err != nil {
		t.Fatal(err)
	}
	s := waitState(t, m, id, StateFailed)
	if s.LastFailure != FailureUnsupported {
		t.Fatalf("failure = %#v", s)
	}
}

func TestConfigureNormalizedClonesAndIsIdempotent(t *testing.T) {
	m := NewManager(ManagerOptions{})
	id, err := m.CreateSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("legacy outline input")
	normalized := []byte("ss://normalized")
	cidrs := []string{"203.0.113.0/24"}
	hosts := []string{"vpn.invalid"}
	profile := RuntimeProfile{
		Summary:          ProfileSummary{Index: 99, Protocol: ProtocolOutline},
		RawTOML:          raw,
		NormalizedFormat: ConfigTransportURL,
		NormalizedConfig: normalized,
		ExcludeCIDRs:     cidrs,
		PreflightHosts:   hosts,
	}
	result, err := m.ConfigureNormalized(context.Background(), id, "legacy-configure", profile)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Profiles) != 1 || result.Profiles[0].Index != 99 {
		t.Fatalf("result = %#v", result)
	}
	raw[0], normalized[0], cidrs[0], hosts[0] = 'X', 'X', "changed", "changed"
	s, err := m.get(id)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	installed := s.profiles[0]
	s.mu.Unlock()
	if string(installed.RawTOML) != "legacy outline input" || string(installed.NormalizedConfig) != "ss://normalized" || installed.ExcludeCIDRs[0] != "203.0.113.0/24" || installed.PreflightHosts[0] != "vpn.invalid" {
		t.Fatalf("profile was not cloned: %#v", installed)
	}
	again, err := m.ConfigureNormalized(context.Background(), id, "legacy-configure", RuntimeProfile{Summary: ProfileSummary{Protocol: ProtocolOutline}, NormalizedFormat: ConfigTransportURL, NormalizedConfig: []byte("different")})
	if err != nil || again.Digest != result.Digest {
		t.Fatalf("idempotent compatibility configure = %#v, %v", again, err)
	}
}

func TestConfigureNormalizedValidatesFormatAndActiveGeneration(t *testing.T) {
	for _, profile := range []RuntimeProfile{
		{Summary: ProfileSummary{Protocol: ProtocolOutline}, NormalizedFormat: ConfigJSON, NormalizedConfig: []byte("x")},
		{Summary: ProfileSummary{Protocol: ProtocolXray}, NormalizedFormat: ConfigTOML, NormalizedConfig: []byte("x")},
		{Summary: ProfileSummary{Protocol: ProtocolTrustTunnel}, NormalizedFormat: ConfigTransportURL, NormalizedConfig: []byte("x")},
		{Summary: ProfileSummary{Protocol: Protocol("unknown")}, NormalizedFormat: ConfigTOML, NormalizedConfig: []byte("x")},
	} {
		m := NewManager(ManagerOptions{})
		id, err := m.CreateSession(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.ConfigureNormalized(context.Background(), id, "bad", profile); CodeOf(err) != FailureInvalidArgument {
			t.Fatalf("invalid profile error = %v", err)
		}
	}

	m := NewManager(ManagerOptions{Runtime: &fakeRuntime{latency: map[int]int64{0: 1}}, Platform: &fakePlatform{}})
	id, err := m.CreateSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	profile := RuntimeProfile{Summary: ProfileSummary{Protocol: ProtocolOutline}, NormalizedFormat: ConfigTransportURL, NormalizedConfig: []byte("ss://normalized")}
	if _, err := m.ConfigureNormalized(context.Background(), id, "configure", profile); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(context.Background(), id, "start", StartTarget{Mode: ProfileIndex, Index: 0}); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, id, StateConnected)
	if _, err := m.ConfigureNormalized(context.Background(), id, "configure-active", profile); CodeOf(err) != FailureConflict {
		t.Fatalf("configure while active = %v", err)
	}
}

func configured(t *testing.T, m *Manager) string {
	t.Helper()
	id, err := m.CreateSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Configure(context.Background(), id, "configure", fixture(t)); err != nil {
		t.Fatal(err)
	}
	return id
}

func waitState(t *testing.T, m *Manager, id string, want State) SnapshotResult {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, err := m.Snapshot(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if got.State == want {
			return got
		}
		time.Sleep(time.Millisecond)
	}
	got, _ := m.Snapshot(context.Background(), id)
	t.Fatalf("did not reach %s: %#v", want, got)
	return SnapshotResult{}
}

type fakeRuntime struct {
	latency      map[int]int64
	blockProbe   chan struct{}
	probeEntered chan struct{}
	stopHook     func()
}

func (r *fakeRuntime) Probe(ctx context.Context, _ SessionRef, p RuntimeProfile) (ProbeResult, error) {
	if r.probeEntered != nil {
		select {
		case r.probeEntered <- struct{}{}:
		default:
			// A notification is already pending.
		}
	}
	if r.blockProbe != nil {
		select {
		case <-r.blockProbe:
		case <-ctx.Done():
			return ProbeResult{}, ctx.Err()
		}
	}
	latency, ok := r.latency[p.Summary.Index]
	if !ok {
		return ProbeResult{}, errors.New("unreachable")
	}
	return ProbeResult{LatencyMillis: latency}, nil
}
func (r *fakeRuntime) Start(context.Context, SessionRef, RuntimeProfile) (RuntimeLease, error) {
	return fakeRuntimeLease{r.stopHook}, nil
}

type fakeRuntimeLease struct{ stop func() }

func (l fakeRuntimeLease) Stop(context.Context) error {
	if l.stop != nil {
		l.stop()
	}
	return nil
}

type cleanupErrorRuntime struct{ err error }

func (*cleanupErrorRuntime) Probe(context.Context, SessionRef, RuntimeProfile) (ProbeResult, error) {
	return ProbeResult{LatencyMillis: 1}, nil
}
func (r *cleanupErrorRuntime) Start(context.Context, SessionRef, RuntimeProfile) (RuntimeLease, error) {
	return cleanupErrorLease{err: r.err}, nil
}

type cleanupErrorLease struct{ err error }

func (l cleanupErrorLease) Stop(context.Context) error { return l.err }

type errorWithLeaseRuntime struct {
	startErr error
	stopErr  error
}

func (errorWithLeaseRuntime) Probe(context.Context, SessionRef, RuntimeProfile) (ProbeResult, error) {
	return ProbeResult{LatencyMillis: 1}, nil
}
func (r errorWithLeaseRuntime) Start(context.Context, SessionRef, RuntimeProfile) (RuntimeLease, error) {
	return cleanupErrorLease{err: r.stopErr}, r.startErr
}

type monitoringRuntime struct {
	failures <-chan struct{}
	stopped  chan uint64
}

func (*monitoringRuntime) Probe(context.Context, SessionRef, RuntimeProfile) (ProbeResult, error) {
	return ProbeResult{LatencyMillis: 1}, nil
}
func (r *monitoringRuntime) Start(_ context.Context, ref SessionRef, _ RuntimeProfile) (RuntimeLease, error) {
	return monitoringRuntimeLease{generation: ref.Generation, failures: r.failures, stopped: r.stopped}, nil
}

type monitoringRuntimeLease struct {
	generation uint64
	failures   <-chan struct{}
	stopped    chan uint64
}

func (l monitoringRuntimeLease) Stop(context.Context) error      { l.stopped <- l.generation; return nil }
func (l monitoringRuntimeLease) HealthFailures() <-chan struct{} { return l.failures }

type eventPlatform struct{ events chan Event }

func (*eventPlatform) PrepareTunnel(context.Context, SessionRef) (PlatformLease, error) {
	return noopLease{}, nil
}
func (*eventPlatform) ProtectSocket(context.Context, SessionRef, int) error { return nil }
func (p *eventPlatform) PublishState(_ context.Context, event Event) error {
	p.events <- event
	return nil
}

func waitForEvent(t *testing.T, events <-chan Event, generation uint64, state State) Event {
	t.Helper()
	for {
		select {
		case event := <-events:
			if event.Generation == generation && event.State == state {
				return event
			}
		case <-time.After(time.Second):
			t.Fatalf("did not receive generation=%d state=%s", generation, state)
		}
	}
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

type fakePlatform struct{ releaseHook func() }

func (p *fakePlatform) PrepareTunnel(context.Context, SessionRef) (PlatformLease, error) {
	return fakePlatformLease{p.releaseHook}, nil
}
func (*fakePlatform) ProtectSocket(context.Context, SessionRef, int) error { return nil }
func (*fakePlatform) PublishState(context.Context, Event) error            { return nil }

type fakePlatformLease struct{ release func() }

func (l fakePlatformLease) Release(context.Context) error {
	if l.release != nil {
		l.release()
	}
	return nil
}

type errorWithLeasePlatform struct {
	prepareErr error
	releaseErr error
}

func (p errorWithLeasePlatform) PrepareTunnel(context.Context, SessionRef) (PlatformLease, error) {
	return cleanupErrorPlatformLease{err: p.releaseErr}, p.prepareErr
}
func (errorWithLeasePlatform) ProtectSocket(context.Context, SessionRef, int) error { return nil }
func (errorWithLeasePlatform) PublishState(context.Context, Event) error            { return nil }

type cleanupErrorPlatformLease struct{ err error }

func (l cleanupErrorPlatformLease) Release(context.Context) error { return l.err }

type recordedOrder struct {
	mu    sync.Mutex
	items []string
}

func (r *recordedOrder) add(item string) {
	r.mu.Lock()
	r.items = append(r.items, item)
	r.mu.Unlock()
}
func (r *recordedOrder) itemsCopy() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.items...)
}

type orderedProbeRuntime struct {
	order   *recordedOrder
	latency map[int]int64
}

func (r *orderedProbeRuntime) Probe(_ context.Context, _ SessionRef, profile RuntimeProfile) (ProbeResult, error) {
	r.order.add(fmt.Sprintf("probe-%d", profile.Summary.Index))
	return ProbeResult{LatencyMillis: r.latency[profile.Summary.Index]}, nil
}
func (r *orderedProbeRuntime) Start(_ context.Context, _ SessionRef, profile RuntimeProfile) (RuntimeLease, error) {
	r.order.add(fmt.Sprintf("start-%d", profile.Summary.Index))
	return fakeRuntimeLease{}, nil
}

type orderedProbePlatform struct {
	order    *recordedOrder
	events   chan Event
	prepared int
}

func (p *orderedProbePlatform) PrepareTunnel(_ context.Context, _ SessionRef) (PlatformLease, error) {
	p.prepared++
	index := p.prepared
	p.order.add(fmt.Sprintf("prepare-%d", index))
	return fakePlatformLease{release: func() { p.order.add(fmt.Sprintf("release-%d", index)) }}, nil
}
func (*orderedProbePlatform) ProtectSocket(context.Context, SessionRef, int) error { return nil }
func (p *orderedProbePlatform) PublishState(_ context.Context, event Event) error {
	p.events <- event
	return nil
}

type releaseSignalPlatform struct{ released chan<- struct{} }

func (p *releaseSignalPlatform) PrepareTunnel(context.Context, SessionRef) (PlatformLease, error) {
	return fakePlatformLease{release: func() { p.released <- struct{}{} }}, nil
}
func (*releaseSignalPlatform) ProtectSocket(context.Context, SessionRef, int) error { return nil }
func (*releaseSignalPlatform) PublishState(context.Context, Event) error            { return nil }

type failingProbePlatform struct{ events chan Event }

func (f failingProbePlatform) PrepareTunnel(context.Context, SessionRef) (PlatformLease, error) {
	return nil, errors.New("prepare probe")
}
func (f failingProbePlatform) ProtectSocket(context.Context, SessionRef, int) error { return nil }
func (f failingProbePlatform) PublishState(_ context.Context, event Event) error {
	f.events <- event
	return nil
}

type orderedPlatform struct {
	mu           sync.Mutex
	events       []Event
	firstEntered chan struct{}
	releaseFirst chan struct{}
	once         sync.Once
}

func (*orderedPlatform) PrepareTunnel(context.Context, SessionRef) (PlatformLease, error) {
	return noopLease{}, nil
}
func (*orderedPlatform) ProtectSocket(context.Context, SessionRef, int) error { return nil }
func (p *orderedPlatform) PublishState(_ context.Context, event Event) error {
	p.once.Do(func() { close(p.firstEntered); <-p.releaseFirst })
	p.mu.Lock()
	p.events = append(p.events, event)
	p.mu.Unlock()
	return nil
}

type blockingStartRuntime struct {
	entered chan<- struct{}
	release <-chan struct{}
	stopped chan<- struct{}
	err     error
}

func (r blockingStartRuntime) Probe(context.Context, SessionRef, RuntimeProfile) (ProbeResult, error) {
	return ProbeResult{LatencyMillis: 1}, nil
}
func (r blockingStartRuntime) Start(context.Context, SessionRef, RuntimeProfile) (RuntimeLease, error) {
	r.entered <- struct{}{}
	<-r.release // deliberately ignores the context, as a misbehaving core could
	return blockingStartLease{stopped: r.stopped, err: r.err}, nil
}

type blockingStartLease struct {
	stopped chan<- struct{}
	err     error
}

func (l blockingStartLease) Stop(context.Context) error { l.stopped <- struct{}{}; return l.err }

type blockingPreparePlatform struct {
	entered  chan<- struct{}
	release  <-chan struct{}
	released chan<- struct{}
	err      error
}

func (p blockingPreparePlatform) PrepareTunnel(context.Context, SessionRef) (PlatformLease, error) {
	p.entered <- struct{}{}
	<-p.release // deliberately ignores cancellation like a misbehaving platform adapter could
	return blockingPrepareLease{released: p.released, err: p.err}, nil
}
func (blockingPreparePlatform) ProtectSocket(context.Context, SessionRef, int) error { return nil }
func (blockingPreparePlatform) PublishState(context.Context, Event) error            { return nil }

type blockingPrepareLease struct {
	released chan<- struct{}
	err      error
}

func (l blockingPrepareLease) Release(context.Context) error {
	l.released <- struct{}{}
	return l.err
}
