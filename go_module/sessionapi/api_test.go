package sessionapi

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
	b, err := os.ReadFile("testdata/mixed_profiles.toml")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestWrappedFailurePreservesCauseAndClassification(t *testing.T) {
	cause := errors.New("exact internal cause")
	wrapped := wrapFailure(FailurePlatform, cause)
	if !errors.Is(wrapped, cause) {
		t.Fatalf("wrapped failure lost its cause: %v", wrapped)
	}
	if wrapped.Error() != "PLATFORM_FAILED: operation failed: exact internal cause" {
		t.Fatalf("failure classification changed: %v", wrapped)
	}
}

func TestAsyncRuntimeFailurePreservesExactMessageInSnapshot(t *testing.T) {
	const exact = "runtime start failed: dial tcp: i/o timeout"
	m := NewManager(ManagerOptions{
		Runtime:  &startErrorRuntime{err: errors.New(exact)},
		Platform: &eventPlatform{events: make(chan StateChange, 32)},
	})
	id := configured(t, m)
	if _, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex, Index: 0}); err != nil {
		t.Fatal(err)
	}

	// Native callbacks only wake observers. The authoritative snapshot retains
	// the complete asynchronous failure message.
	want := "RUNTIME_FAILED: operation failed: " + exact
	event := waitForEvent(t, m.platform.(*eventPlatform).events, 1, StateFailed)
	if event.Failure != FailureRuntime {
		t.Fatalf("async failure callback = %#v, want failure %s", event, FailureRuntime)
	}
	snapshot := waitState(t, m, id, StateFailed)
	if snapshot.LastFailure != FailureRuntime || snapshot.LastFailureMessage != want {
		t.Fatalf("async failure snapshot = %#v, want message %q", snapshot, want)
	}
}

func TestConfigurePreservesMixedSourceOrder(t *testing.T) {
	m := NewManager(ManagerOptions{})
	id, err := currentSessionForTest(t, m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := configureForTest(t, m, id, fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Profiles) != 4 {
		t.Fatalf("profiles = %#v", got.Profiles)
	}
	want := []Protocol{ProtocolOutline, ProtocolXray, ProtocolTrustTunnel, ProtocolOutline}
	for i := range want {
		if int(got.Profiles[i].Index) != i || got.Profiles[i].Protocol != want[i] {
			t.Fatalf("profile %d = %#v", i, got.Profiles[i])
		}
	}
	if len(got.Warnings) != 0 {
		t.Fatalf("warnings = %#v", got.Warnings)
	}
	if got.Digest == "" {
		t.Fatal("empty digest")
	}
	if _, err := configureForTest(t, m, id, []byte("[Outline]\nServer='x'")); CodeOf(err) != FailureMalformedConfig {
		t.Fatalf("bad config error = %v", err)
	}
}

func TestSnapshotCarriesOnlyAcceptedConfigurationURL(t *testing.T) {
	m := NewManager(ManagerOptions{Loader: acceptedSourceURLLoader{}})
	initial, err := m.Snapshot(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	configuredURL, err := m.Configure(context.Background(), initial.SessionID, initial.Sequence, []byte("https://configs.invalid/profile"))
	if err != nil {
		t.Fatal(err)
	}
	accepted := snapshotForTest(t, m, initial.SessionID)
	if accepted.SourceKind != ConfigSourceURL || accepted.SourceURL != "https://configs.invalid/profile" {
		t.Fatalf("accepted URL snapshot = %#v", accepted)
	}
	if _, err := m.Configure(context.Background(), initial.SessionID, configuredURL.Sequence, []byte("https://configs.invalid/bad")); err == nil {
		t.Fatal("malformed URL configuration unexpectedly succeeded")
	}
	unchanged := snapshotForTest(t, m, initial.SessionID)
	if unchanged.SourceURL != accepted.SourceURL {
		t.Fatalf("failed configuration replaced accepted URL: got %q, want %q", unchanged.SourceURL, accepted.SourceURL)
	}
	if _, err := m.Configure(context.Background(), initial.SessionID, unchanged.Sequence, fixture(t)); err != nil {
		t.Fatal(err)
	}
	inline := snapshotForTest(t, m, initial.SessionID)
	if inline.SourceURL != "" || inline.SourceKind != ConfigSourceInline {
		t.Fatalf("inline configuration retained stale URL: %#v", inline)
	}
}

type acceptedSourceURLLoader struct{}

func (acceptedSourceURLLoader) Load(_ context.Context, source []byte) (LoadedConfig, error) {
	value := string(source)
	if strings.HasSuffix(value, "/bad") {
		return LoadedConfig{Raw: []byte("not a configuration"), Kind: ConfigSourceURL, SourceURL: value}, nil
	}
	if strings.HasPrefix(value, "https://") {
		return LoadedConfig{Raw: []byte(loaderTestConfig), Kind: ConfigSourceURL, SourceURL: value}, nil
	}
	return LoadedConfig{Raw: append([]byte(nil), source...), Kind: ConfigSourceInline}, nil
}

func TestConfigureRejectsRemovedCloakProfiles(t *testing.T) {
	raw := strings.Join([]string{
		"[[Outline]]", `Description = "supported-before"`, `Server = "198.51.100.20"`, "Port = 443", `Password = "synthetic-password"`,
		"", "[[Xray]]", `Description = "legacy-cloak"`, "Cloak = true", `Server = "cloak.invalid"`, `Password = "do-not-return"`,
		"", "[[TrustTunnel]]", `Description = "supported-after"`, `vpn_mode = "general"`, "[TrustTunnel.endpoint]", `hostname = "vpn.invalid"`, `addresses = ["198.51.100.21:443"]`, `username = "synthetic-user"`, `password = "synthetic-password"`, "[TrustTunnel.listener.socks]", `address = "127.0.0.1:10808"`,
	}, "\n")
	m := NewManager(ManagerOptions{})
	id, err := currentSessionForTest(t, m)
	if err != nil {
		t.Fatal(err)
	}
	_, err = configureForTest(t, m, id, []byte(raw))
	if CodeOf(err) != FailureUnsupported || err.Error() != "UNSUPPORTED: configuration contains a removed Cloak profile" {
		t.Fatalf("removed Cloak result = %v", err)
	}
}

func TestConfigureRejectsMultipleAndAllCloakInputsBeforeExecution(t *testing.T) {
	const syntheticURL = "https://cloak.example.invalid/private/profile"
	const syntheticEndpoint = "198.51.100.99:8443"
	const syntheticCredential = "cloak-secret-token"
	tests := map[string]string{
		"multiple Cloak sections": strings.Join([]string{
			"[[Xray]]", "Cloak = true", `outbounds = [{"address" = "` + syntheticEndpoint + `"}]`,
			"", "[[Outline]]", "Cloak = true", `Server = "` + syntheticURL + `"`, `Password = "` + syntheticCredential + `"`, "Port = 443",
		}, "\n"),
		"all Cloak sections": strings.Join([]string{
			"[[Outline]]", "Cloak = true", `Server = "` + syntheticURL + `"`, `Password = "` + syntheticCredential + `"`, "Port = 443",
			"", "[[TrustTunnel]]", "Cloak = true", `hostname = "` + syntheticEndpoint + `"`, `password = "` + syntheticCredential + `"`, "[TrustTunnel.endpoint]", `addresses = ["` + syntheticEndpoint + `"]`,
		}, "\n"),
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			runtime := &countingRuntime{}
			m := NewManager(ManagerOptions{Runtime: runtime, Platform: &fakePlatform{}})
			id, err := currentSessionForTest(t, m)
			if err != nil {
				t.Fatal(err)
			}
			result, configureErr := configureForTest(t, m, id, []byte(raw))
			if CodeOf(configureErr) != FailureUnsupported || configureErr.Error() != "UNSUPPORTED: configuration contains a removed Cloak profile" {
				t.Fatalf("Cloak result = %#v, %v", result, configureErr)
			}
			if result.Digest != "" || len(result.Profiles) != 0 || len(result.Warnings) != 0 {
				t.Fatalf("rejected input returned configuration data: %#v", result)
			}
			if _, startErr := startForTest(t, m, id, StartTarget{Mode: AutoSelect}); CodeOf(startErr) != FailureNotConfigured {
				t.Fatalf("start after rejected configure = %v", startErr)
			}
			if runtime.probeCalls != 0 || runtime.startCalls != 0 {
				t.Fatalf("runtime executed for rejected input: probes=%d starts=%d", runtime.probeCalls, runtime.startCalls)
			}
			snapshot, err := m.Snapshot(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.Configured || snapshot.State != StateIdle || snapshot.LastFailure != "" {
				t.Fatalf("rejected input changed session state: %#v", snapshot)
			}
		})
	}
}

func TestWatchStartsWithSnapshotAndReportsResetAsCurrentState(t *testing.T) {
	m := NewManager(ManagerOptions{})
	id, err := currentSessionForTest(t, m)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates, closeSubscription, err := m.Watch(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSubscription()
	initial := <-updates
	if initial.SessionID != id || initial.State != StateIdle || initial.Sequence == 0 {
		t.Fatalf("initial snapshot = %#v", initial)
	}
	if _, err := configureForTest(t, m, id, fixture(t)); err != nil {
		t.Fatal(err)
	}
	configured := <-updates
	if configured.SessionID != id || configured.State != StateConfigured || configured.Sequence <= initial.Sequence {
		t.Fatalf("configured snapshot = %#v", configured)
	}
	if err := resetForTest(t, m, id); err != nil {
		t.Fatal(err)
	}
	reset := <-updates
	if reset.SessionID != id || reset.State != StateIdle || reset.Configured || reset.Sequence <= configured.Sequence {
		t.Fatalf("reset snapshot = %#v", reset)
	}
}

func TestOneOwnerRetainsConfigurationAcrossIdleAndRecovery(t *testing.T) {
	m := NewManager(ManagerOptions{Runtime: &fakeRuntime{latency: map[int32]int64{0: 1}}, Platform: &fakePlatform{}})
	id := configured(t, m)
	configuredSnapshot, err := m.Snapshot(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	first, err := startForTest(t, m, id, StartTarget{Mode: AutoSelect})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, id, StateConnected)
	if _, stopErr := m.Stop(context.Background(), id, first.Generation); stopErr != nil {
		t.Fatal(stopErr)
	}
	idle := waitState(t, m, id, StateIdle)
	if idle.SessionID != id || !idle.Configured || idle.Digest != configuredSnapshot.Digest || len(idle.Profiles) != len(configuredSnapshot.Profiles) {
		t.Fatalf("idle owner lost accepted configuration: before=%#v after=%#v", configuredSnapshot, idle)
	}
	attached, err := m.Snapshot(context.Background(), "")
	if err != nil || attached.SessionID != id || attached.Generation != first.Generation {
		t.Fatalf("reattachment = %#v, %v", attached, err)
	}
	second, err := startForTest(t, m, id, StartTarget{Mode: AutoSelect})
	if err != nil || second.Generation != first.Generation+1 {
		t.Fatalf("restart after ordinary stop = %#v, %v; first generation %d", second, err, first.Generation)
	}
	waitState(t, m, id, StateConnected)

	restarted := NewManager(ManagerOptions{})
	if _, err := restarted.Snapshot(context.Background(), id); CodeOf(err) != FailureNotFound {
		t.Fatalf("old owner identity after service restart = %v", err)
	}
}

func TestNormalizationAndResults(t *testing.T) {
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
	result, err := configureForTest(t, m, id, []byte("not = [valid"))
	if err == nil || result.Digest != "" || CodeOf(err) != FailureMalformedConfig {
		t.Fatalf("malformed result=%#v err=%v", result, err)
	}
}

func TestValidateConfigIsStatelessAndSnapshotCarriesAcceptedMetadata(t *testing.T) {
	m := NewManager(ManagerOptions{})
	before := snapshotForTest(t, m, "")
	assertValidationIsStateless(t, m, before)
	configured := acceptAndCheckConfiguration(t, m, before)
	assertInvalidConfigurationDoesNotReplace(t, m, before, configured)
	assertSnapshotCollectionsAreCopied(t, m, before)
}

func assertValidationIsStateless(t *testing.T, m *Manager, before SnapshotResult) {
	t.Helper()
	validated, err := m.ValidateConfig(context.Background(), fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if validated.Digest == "" || validated.SourceKind != ConfigSourceInline || len(validated.Profiles) != 4 {
		t.Fatalf("validation result = %#v", validated)
	}
	if _, validationErr := m.ValidateConfig(context.Background(), []byte("not TOML")); CodeOf(validationErr) != FailureMalformedConfig {
		t.Fatalf("invalid validation error = %v", validationErr)
	}
	afterValidation := snapshotForTest(t, m, "")
	if afterValidation.SessionID != before.SessionID || afterValidation.Sequence != before.Sequence || afterValidation.Generation != before.Generation || afterValidation.State != before.State || afterValidation.Configured != before.Configured {
		t.Fatalf("validation mutated the owner: before=%#v after=%#v", before, afterValidation)
	}
}

func acceptAndCheckConfiguration(t *testing.T, m *Manager, before SnapshotResult) SnapshotResult {
	t.Helper()
	accepted, err := configureForTest(t, m, before.SessionID, fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	configured := snapshotForTest(t, m, before.SessionID)
	if configured.State != StateConfigured || !configured.Configured || configured.Digest != accepted.Digest || configured.SourceKind != accepted.SourceKind || len(configured.Profiles) != 4 || configured.Sequence <= before.Sequence {
		t.Fatalf("configured metadata = %#v; configure=%#v before=%#v", configured, accepted, before)
	}
	return configured
}

func assertInvalidConfigurationDoesNotReplace(t *testing.T, m *Manager, before, configured SnapshotResult) {
	t.Helper()
	if _, validationErr := m.ValidateConfig(context.Background(), []byte("not TOML")); CodeOf(validationErr) != FailureMalformedConfig {
		t.Fatalf("invalid validation after configure = %v", validationErr)
	}
	if _, configureErr := configureForTest(t, m, before.SessionID, []byte("not TOML")); CodeOf(configureErr) != FailureMalformedConfig {
		t.Fatalf("invalid replacement configuration = %v", configureErr)
	}
	unchanged := snapshotForTest(t, m, before.SessionID)
	if unchanged.Sequence != configured.Sequence || unchanged.Digest != configured.Digest || unchanged.State != StateConfigured || !unchanged.Configured || unchanged.SourceKind != ConfigSourceInline || len(unchanged.Profiles) != 4 {
		t.Fatalf("failed validation/configuration replaced accepted config: %#v", unchanged)
	}
}

func assertSnapshotCollectionsAreCopied(t *testing.T, m *Manager, before SnapshotResult) {
	t.Helper()
	unchanged := snapshotForTest(t, m, before.SessionID)
	// Snapshot collections and profile pointers are copies, not mutable views
	// into the manager's retained configuration.
	unchanged.Profiles[0].Description = "mutated by caller"
	if unchanged.ActiveProfile != nil {
		unchanged.ActiveProfile.Description = "mutated by caller"
	}
	verified := snapshotForTest(t, m, before.SessionID)
	if verified.Profiles[0].Description == "mutated by caller" {
		t.Fatalf("snapshot exposed mutable profile storage: %#v", verified.Profiles[0])
	}
}

func TestManagerPreservesSafeHTTPSRequirementForSubscriptionURLs(t *testing.T) {
	const raw = "http://username:password@example.invalid/private-profile"
	const want = "INVALID_ARGUMENT: configuration URL must use HTTPS"
	m := NewManager(ManagerOptions{})

	if _, err := m.ValidateConfig(context.Background(), []byte(raw)); err == nil || err.Error() != want {
		t.Fatalf("ValidateConfig HTTP URL error = %v, want %q", err, want)
	}
	initial, err := m.Snapshot(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Configure(context.Background(), initial.SessionID, initial.Sequence, []byte(raw)); err == nil || err.Error() != want {
		t.Fatalf("Configure HTTP URL error = %v, want %q", err, want)
	}
}

func TestStaleRevisionCannotConfigureStartOrReset(t *testing.T) {
	m := NewManager(ManagerOptions{Runtime: &fakeRuntime{latency: map[int32]int64{0: 1}}, Platform: &fakePlatform{}})
	initial, err := m.Snapshot(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := m.Configure(context.Background(), initial.SessionID, initial.Sequence, fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	current, err := m.Snapshot(context.Background(), initial.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Sequence != accepted.Sequence {
		t.Fatalf("configure revision = %d, snapshot revision = %d", accepted.Sequence, current.Sequence)
	}
	if _, configureErr := m.Configure(context.Background(), initial.SessionID, initial.Sequence, fixture(t)); CodeOf(configureErr) != FailureConflict {
		t.Fatalf("stale configure = %v", configureErr)
	}
	if _, startErr := m.Start(context.Background(), initial.SessionID, initial.Sequence, StartTarget{Mode: AutoSelect}); CodeOf(startErr) != FailureConflict {
		t.Fatalf("stale start = %v", startErr)
	}
	if _, resetErr := m.Reset(context.Background(), initial.SessionID, initial.Sequence); CodeOf(resetErr) != FailureConflict {
		t.Fatalf("stale reset = %v", resetErr)
	}
	after, err := m.Snapshot(context.Background(), initial.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Sequence != current.Sequence || after.State != StateConfigured || !after.Configured || after.Digest != current.Digest {
		t.Fatalf("stale calls mutated owner: before=%#v after=%#v", current, after)
	}
}

func TestConfigureAndResetAtSameRevisionOnlyOneMutationWins(t *testing.T) {
	raw := fixture(t)
	for i := 0; i < 50; i++ {
		m := NewManager(ManagerOptions{})
		initial, err := m.Snapshot(context.Background(), "")
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		var configureErr, resetErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, configureErr = m.Configure(context.Background(), initial.SessionID, initial.Sequence, raw)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, resetErr = m.Reset(context.Background(), initial.SessionID, initial.Sequence)
		}()
		close(start)
		wg.Wait()
		if (configureErr == nil) == (resetErr == nil) {
			t.Fatalf("iteration %d: expected exactly one mutation to win, configure=%v reset=%v", i, configureErr, resetErr)
		}
		if configureErr != nil && CodeOf(configureErr) != FailureConflict {
			t.Fatalf("iteration %d: configure error = %v", i, configureErr)
		}
		if resetErr != nil && CodeOf(resetErr) != FailureConflict {
			t.Fatalf("iteration %d: reset error = %v", i, resetErr)
		}
	}
}

func TestWatchCoalescesMoreThanSixtyFourChangesAndCancellationUnregisters(t *testing.T) {
	m := NewManager(ManagerOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	id, err := currentSessionForTest(t, m)
	if err != nil {
		t.Fatal(err)
	}
	updates, closeSubscription, err := m.Watch(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSubscription()
	initial := <-updates
	if initial.SessionID != id || initial.State != StateIdle {
		t.Fatalf("initial snapshot = %#v", initial)
	}

	const changes = 96
	configureRepeatedly(t, m, id, fixture(t), changes)
	latest := snapshotForTest(t, m, id)
	if latest.Sequence != initial.Sequence+changes || latest.State != StateConfigured || !latest.Configured {
		t.Fatalf("latest snapshot after %d changes = %#v", changes, latest)
	}
	assertCoalescedWatchUpdate(t, updates, latest)
	cancel()
	assertWatchClosed(t, updates)
}

func configureRepeatedly(t *testing.T, m *Manager, id string, raw []byte, changes int) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		for i := 0; i < changes; i++ {
			current, snapshotErr := m.Snapshot(context.Background(), id)
			if snapshotErr != nil {
				done <- snapshotErr
				return
			}
			if _, configureErr := m.Configure(context.Background(), id, current.Sequence, raw); configureErr != nil {
				done <- configureErr
				return
			}
		}
		done <- nil
	}()
	select {
	case workerErr := <-done:
		if workerErr != nil {
			t.Fatal(workerErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("slow watcher blocked repeated configuration changes")
	}
}

func assertCoalescedWatchUpdate(t *testing.T, updates <-chan SnapshotResult, latest SnapshotResult) {
	t.Helper()
	select {
	case coalesced := <-updates:
		if coalesced.Sequence != latest.Sequence || coalesced.Digest != latest.Digest || coalesced.State != latest.State {
			t.Fatalf("slow watcher got stale snapshot %#v; latest %#v", coalesced, latest)
		}
	default:
		t.Fatal("watcher did not receive a coalesced update")
	}
}

func assertWatchClosed(t *testing.T, updates <-chan SnapshotResult) {
	t.Helper()
	select {
	case _, open := <-updates:
		if open {
			// The update may have raced with cancellation. The next receive must
			// observe closure once the cancellation handler unregisters it.
			select {
			case _, open = <-updates:
			case <-time.After(time.Second):
				t.Fatal("canceled watch remained registered")
			}
		}
		if open {
			t.Fatal("canceled watch channel remained open")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled watch did not close")
	}
}

func TestSlowWatcherDoesNotBlockStop(t *testing.T) {
	m := NewManager(ManagerOptions{Runtime: &fakeRuntime{latency: map[int32]int64{0: 1}}, Platform: &fakePlatform{}})
	id := configured(t, m)
	updates, closeSubscription, err := m.Watch(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSubscription()
	<-updates // leave the subscriber behind while the lifecycle advances
	start, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex, Index: 0})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, id, StateConnected)
	if _, err := m.Stop(context.Background(), id, start.Generation); err != nil {
		t.Fatal(err)
	}
	if stopped := waitState(t, m, id, StateIdle); stopped.Generation != start.Generation {
		t.Fatalf("stopped generation = %#v", stopped)
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
			id, err := currentSessionForTest(t, m)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := configureForTest(t, m, id, []byte(raw)); CodeOf(err) != FailureMalformedConfig {
				t.Fatalf("Configure error = %v", err)
			}
		})
	}
}

func TestAutoSelectionUsesLatencyThenSourceOrder(t *testing.T) {
	r := &fakeRuntime{latency: map[int32]int64{0: 50, 1: 10, 2: 10, 3: 60}}
	platform := &eventPlatform{events: make(chan StateChange, 32)}
	m := NewManager(ManagerOptions{Runtime: r, Platform: platform})
	id := configured(t, m)
	start, err := startForTest(t, m, id, StartTarget{Mode: AutoSelect})
	if err != nil {
		t.Fatal(err)
	}
	s := waitState(t, m, id, StateConnected)
	if s.ActiveProfile == nil || s.ActiveProfile.Index != 1 {
		t.Fatalf("active = %#v", s.ActiveProfile)
	}
	if s.Generation != start.Generation || s.Sequence <= 1 {
		t.Fatalf("connected snapshot = %#v; start = %#v", s, start)
	}
	if _, stopErr := m.Stop(context.Background(), id, start.Generation+1); CodeOf(stopErr) != FailureStaleGeneration {
		t.Fatalf("stale stop = %v", stopErr)
	}
	change := waitForEvent(t, platform.events, start.Generation, StateConnected)
	if change.SessionID != id || change.Failure != "" {
		t.Fatalf("state callback = %#v", change)
	}
}

func TestStopReportsCleanupFailureAndBlocksRestart(t *testing.T) {
	runtime := &cleanupErrorRuntime{err: errors.New("cleanup failed")}
	m := NewManager(ManagerOptions{Runtime: runtime, Platform: &fakePlatform{}})
	id := configured(t, m)
	start, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex, Index: 0})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, id, StateConnected)
	if _, err := m.Stop(context.Background(), id, start.Generation); err != nil {
		t.Fatal(err)
	}
	snapshot := waitState(t, m, id, StateFailed)
	if !snapshot.CleanupComplete || snapshot.LastFailure != FailureCleanup {
		t.Fatalf("cleanup snapshot=%#v", snapshot)
	}
	if _, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex, Index: 0}); CodeOf(err) != FailureConflict {
		t.Fatalf("restart error=%v, want %s", err, FailureConflict)
	}
	if _, err := configureForTest(t, m, id, fixture(t)); CodeOf(err) != FailureConflict {
		t.Fatalf("configure after cleanup failure error=%v, want %s", err, FailureConflict)
	}
	if err := resetForTest(t, m, id); CodeOf(err) != FailureConflict {
		t.Fatalf("destroy after cleanup failure error=%v, want %s", err, FailureConflict)
	}
}

func TestStopAcknowledgesAlreadyCleanedTerminalGeneration(t *testing.T) {
	failures := make(chan struct{}, 1)
	runtime := &monitoringRuntime{failures: failures, stopped: make(chan uint64, 2)}
	platform := &eventPlatform{events: make(chan StateChange, 32)}
	m := NewManager(ManagerOptions{Runtime: runtime, Platform: platform})
	id := configured(t, m)
	start, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex, Index: 0})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, id, StateConnected)
	failures <- struct{}{}
	snapshot := waitState(t, m, id, StateFailed)
	if !snapshot.CleanupComplete || snapshot.LastFailure != FailureRuntime {
		t.Fatalf("health-failure snapshot=%#v", snapshot)
	}
	if _, err := m.Stop(context.Background(), id, start.Generation); err != nil {
		t.Fatalf("already-cleaned terminal stop: %v", err)
	}
	if _, err := m.Stop(context.Background(), id, start.Generation); err != nil {
		t.Fatalf("repeated already-cleaned terminal stop: %v", err)
	}
}

func TestPlatformAcquisitionErrorStillOwnsAndReportsReturnedLeaseCleanup(t *testing.T) {
	want := errors.New("platform rollback failed")
	m := NewManager(ManagerOptions{
		Runtime:  &fakeRuntime{latency: map[int32]int64{0: 1}},
		Platform: errorWithLeasePlatform{prepareErr: errors.New("prepare failed"), releaseErr: want},
	})
	id := configured(t, m)
	if _, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex, Index: 0}); err != nil {
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
	if _, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex, Index: 0}); err != nil {
		t.Fatal(err)
	}
	snapshot := waitState(t, m, id, StateFailed)
	if !snapshot.CleanupComplete || snapshot.LastFailure != FailureCleanup {
		t.Fatalf("cleanup snapshot=%#v", snapshot)
	}
}

func TestAutoSelectionProbesWithFreshPlatformLeaseBeforeEachRuntimeProbe(t *testing.T) {
	order := &recordedOrder{}
	runtime := &orderedProbeRuntime{order: order, latency: map[int32]int64{0: 40, 1: 10, 2: 30, 3: 20}}
	platform := &orderedProbePlatform{order: order, events: make(chan StateChange, 32)}
	m := NewManager(ManagerOptions{Runtime: runtime, Platform: platform})
	id := configured(t, m)
	if _, err := startForTest(t, m, id, StartTarget{Mode: AutoSelect}); err != nil {
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
	runtime := &fakeRuntime{latency: map[int32]int64{0: 1}, blockProbe: make(chan struct{}), probeEntered: entered}
	platform := &releaseSignalPlatform{released: released}
	m := NewManager(ManagerOptions{Runtime: runtime, Platform: platform})
	id := configured(t, m)
	start, err := startForTest(t, m, id, StartTarget{Mode: AutoSelect})
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	if _, err := m.Stop(context.Background(), id, start.Generation); err != nil {
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
	platform := failingProbePlatform{events: make(chan StateChange, 8)}
	m := NewManager(ManagerOptions{Runtime: &fakeRuntime{latency: map[int32]int64{0: 1}}, Platform: platform})
	id := configured(t, m)
	if _, err := startForTest(t, m, id, StartTarget{Mode: AutoSelect}); err != nil {
		t.Fatal(err)
	}
	event := waitForEvent(t, platform.events, 1, StateFailed)
	if event.Failure != FailurePlatform {
		t.Fatalf("failure=%s, want %s", event.Failure, FailurePlatform)
	}
}

func TestStopDuringProbePreventsLateConnectedAndAllowsRestartAfterCleanup(t *testing.T) {
	r := &fakeRuntime{latency: map[int32]int64{0: 1}, blockProbe: make(chan struct{}), probeEntered: make(chan struct{}, 1)}
	p := &fakePlatform{}
	m := NewManager(ManagerOptions{Runtime: r, Platform: p})
	id := configured(t, m)
	first, err := startForTest(t, m, id, StartTarget{Mode: AutoSelect})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.probeEntered:
	case <-time.After(time.Second):
		t.Fatal("probe did not start")
	}
	if _, stopErr := m.Stop(context.Background(), id, first.Generation); stopErr != nil {
		t.Fatal(stopErr)
	}
	waitState(t, m, id, StateIdle)
	close(r.blockProbe)
	time.Sleep(10 * time.Millisecond) // permits the deliberately stale probe completion to return
	if got, _ := m.Snapshot(context.Background(), id); got.State == StateConnected {
		t.Fatalf("stale completion connected: %#v", got)
	}
	second, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex, Index: 0})
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
	r := &fakeRuntime{latency: map[int32]int64{0: 1}, stopHook: func() { orderMu.Lock(); order = append(order, "runtime"); orderMu.Unlock() }}
	p := &fakePlatform{releaseHook: func() { orderMu.Lock(); order = append(order, "platform"); orderMu.Unlock() }}
	m := NewManager(ManagerOptions{Runtime: r, Platform: p})
	id := configured(t, m)
	first, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex, Index: 0})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, id, StateConnected)
	if _, err := m.Stop(context.Background(), id, first.Generation); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, id, StateIdle)
	orderMu.Lock()
	got := append([]string(nil), order...)
	orderMu.Unlock()
	if len(got) != 2 || got[0] != "runtime" || got[1] != "platform" {
		t.Fatalf("cleanup order = %#v", got)
	}
	if _, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex, Index: 0}); err != nil {
		t.Fatalf("restart after cleanup: %v", err)
	}
}

func TestRuntimeOwnedHealthFailureCleansUpBeforeAutoFailover(t *testing.T) {
	failures := make(chan struct{}, 1)
	defer close(failures)
	runtime := &monitoringRuntime{failures: failures, stopped: make(chan uint64, 2)}
	platform := &eventPlatform{events: make(chan StateChange, 32)}
	m := NewManager(ManagerOptions{Runtime: runtime, Platform: platform})
	id, err := currentSessionForTest(t, m)
	if err != nil {
		t.Fatal(err)
	}
	if _, configureErr := configureForTest(t, m, id, fixture(t)); configureErr != nil {
		t.Fatal(configureErr)
	}
	first, err := startForTest(t, m, id, StartTarget{Mode: AutoSelect})
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
	platform := &eventPlatform{events: make(chan StateChange, 32)}
	m := NewManager(ManagerOptions{Runtime: runtime, Platform: platform})
	id, err := currentSessionForTest(t, m)
	if err != nil {
		t.Fatal(err)
	}
	if _, configureErr := configureForTest(t, m, id, fixture(t)); configureErr != nil {
		t.Fatal(configureErr)
	}
	first, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex, Index: 0})
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
	start, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex, Index: 0})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("runtime start did not begin")
	}
	if _, err := m.Stop(context.Background(), id, start.Generation); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Snapshot(context.Background(), id); got.State != StateStopping || got.CleanupComplete {
		t.Fatalf("stop released early: %#v", got)
	}
	if _, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex, Index: 0}); CodeOf(err) != FailureConflict {
		t.Fatalf("restart while start blocked = %v", err)
	}
	close(releaseStart)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("late runtime lease was not stopped")
	}
	waitState(t, m, id, StateIdle)
	if _, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex, Index: 0}); err != nil {
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
	start, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex, Index: 0})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("runtime start did not begin")
	}
	if _, err := m.Stop(context.Background(), id, start.Generation); err != nil {
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
	m := NewManager(ManagerOptions{Runtime: &fakeRuntime{latency: map[int32]int64{0: 1}}, Platform: platform})
	id := configured(t, m)
	start, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex, Index: 0})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("platform preparation did not begin")
	}
	if _, err := m.Stop(context.Background(), id, start.Generation); err != nil {
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
	if _, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex, Index: 0}); err != nil {
		t.Fatal(err)
	}
	s := waitState(t, m, id, StateFailed)
	if s.LastFailure != FailureUnsupported {
		t.Fatalf("failure = %#v", s)
	}
}

func configured(t *testing.T, m *Manager) string {
	t.Helper()
	id, err := currentSessionForTest(t, m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = configureForTest(t, m, id, fixture(t)); err != nil {
		t.Fatal(err)
	}
	return id
}

func currentSessionForTest(t *testing.T, m *Manager) (string, error) {
	t.Helper()
	snapshot, err := m.Snapshot(context.Background(), "")
	if err != nil {
		return "", err
	}
	return snapshot.SessionID, nil
}

func snapshotForTest(t *testing.T, m *Manager, id string) SnapshotResult {
	t.Helper()
	snapshot, err := m.Snapshot(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func configureForTest(t *testing.T, m *Manager, id string, raw []byte) (ConfigureResult, error) {
	t.Helper()
	snapshot, err := m.Snapshot(context.Background(), id)
	if err != nil {
		return ConfigureResult{}, err
	}
	return m.Configure(context.Background(), id, snapshot.Sequence, raw)
}

func startForTest(t *testing.T, m *Manager, id string, target StartTarget) (StartResult, error) {
	t.Helper()
	snapshot, err := m.Snapshot(context.Background(), id)
	if err != nil {
		return StartResult{}, err
	}
	return m.Start(context.Background(), id, snapshot.Sequence, target)
}

func resetForTest(t *testing.T, m *Manager, id string) error {
	t.Helper()
	snapshot, err := m.Snapshot(context.Background(), id)
	if err != nil {
		return err
	}
	_, err = m.Reset(context.Background(), id, snapshot.Sequence)
	return err
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
	latency      map[int32]int64
	blockProbe   chan struct{}
	probeEntered chan struct{}
	stopHook     func()
}

type startErrorRuntime struct{ err error }

func (r *startErrorRuntime) Probe(context.Context, SessionRef, RuntimeProfile) (ProbeResult, error) {
	return ProbeResult{LatencyMillis: 1}, nil
}
func (r *startErrorRuntime) Start(context.Context, SessionRef, RuntimeProfile) (RuntimeLease, error) {
	return nil, r.err
}

type countingRuntime struct {
	probeCalls int
	startCalls int
}

func (r *countingRuntime) Probe(context.Context, SessionRef, RuntimeProfile) (ProbeResult, error) {
	r.probeCalls++
	return ProbeResult{LatencyMillis: 1}, nil
}

func (r *countingRuntime) Start(context.Context, SessionRef, RuntimeProfile) (RuntimeLease, error) {
	r.startCalls++
	return fakeRuntimeLease{}, nil
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

type eventPlatform struct{ events chan StateChange }

func (*eventPlatform) PrepareTunnel(context.Context, SessionRef) (PlatformLease, error) {
	return noopLease{}, nil
}
func (*eventPlatform) ProtectSocket(context.Context, SessionRef, int) error { return nil }
func (p *eventPlatform) PublishState(_ context.Context, event StateChange) {
	p.events <- event
}

func waitForEvent(t *testing.T, events <-chan StateChange, generation uint64, state State) StateChange {
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
func (*fakePlatform) PublishState(context.Context, StateChange)            {}

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
func (errorWithLeasePlatform) PublishState(context.Context, StateChange)            {}

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
	latency map[int32]int64
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
	events   chan StateChange
	prepared int
}

func (p *orderedProbePlatform) PrepareTunnel(_ context.Context, _ SessionRef) (PlatformLease, error) {
	p.prepared++
	index := p.prepared
	p.order.add(fmt.Sprintf("prepare-%d", index))
	return fakePlatformLease{release: func() { p.order.add(fmt.Sprintf("release-%d", index)) }}, nil
}
func (*orderedProbePlatform) ProtectSocket(context.Context, SessionRef, int) error { return nil }
func (p *orderedProbePlatform) PublishState(_ context.Context, event StateChange) {
	p.events <- event
}

type releaseSignalPlatform struct{ released chan<- struct{} }

func (p *releaseSignalPlatform) PrepareTunnel(context.Context, SessionRef) (PlatformLease, error) {
	return fakePlatformLease{release: func() { p.released <- struct{}{} }}, nil
}
func (*releaseSignalPlatform) ProtectSocket(context.Context, SessionRef, int) error { return nil }
func (*releaseSignalPlatform) PublishState(context.Context, StateChange)            {}

type failingProbePlatform struct{ events chan StateChange }

func (f failingProbePlatform) PrepareTunnel(context.Context, SessionRef) (PlatformLease, error) {
	return nil, errors.New("prepare probe")
}
func (f failingProbePlatform) ProtectSocket(context.Context, SessionRef, int) error { return nil }
func (f failingProbePlatform) PublishState(_ context.Context, event StateChange) {
	f.events <- event
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
func (blockingPreparePlatform) PublishState(context.Context, StateChange)            {}

type blockingPrepareLease struct {
	released chan<- struct{}
	err      error
}

func (l blockingPrepareLease) Release(context.Context) error {
	l.released <- struct{}{}
	return l.err
}
