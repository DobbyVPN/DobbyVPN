package sessionapi

import (
	"context"
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
	if got.Digest == "" {
		t.Fatal("empty digest")
	}
	malformedTOML := []byte("[[Outline]\nServer = 'vpn.invalid'\n")
	if _, err := configureForTest(t, m, id, malformedTOML); CodeOf(err) != FailureMalformedConfig {
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

func TestStartAcceptsChangedSourceBeforeRuntimeFailure(t *testing.T) {
	store := &managerTestSourceStore{}
	m := NewManager(ManagerOptions{
		Loader:      acceptedSourceURLLoader{},
		SourceStore: store,
		Runtime:     &startErrorRuntime{err: errors.New("synthetic dial failure")},
		Platform:    &fakePlatform{},
	})
	initial := snapshotForTest(t, m, "")
	started, err := m.Start(context.Background(), initial.SessionID, initial.Sequence, StartTarget{
		Mode: AutoSelect, Source: []byte("https://configs.invalid/new"),
	})
	if err != nil {
		t.Fatal(err)
	}
	failed := waitState(t, m, initial.SessionID, StateFailed)
	if failed.Generation != started.Generation || failed.SourceURL != "https://configs.invalid/new" ||
		failed.SourceKind != ConfigSourceURL || string(store.value) != failed.SourceURL {
		t.Fatalf("changed source was not retained after runtime failure: %#v, stored %q", failed, store.value)
	}
	s, err := m.get(initial.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeTarget.Source != nil {
		t.Fatal("recovery target retained source bytes")
	}
}

func TestStartRejectsIndexedChangedSourceWithoutReplacingAcceptedConfiguration(t *testing.T) {
	store := &managerTestSourceStore{}
	m := NewManager(ManagerOptions{Loader: acceptedSourceURLLoader{}, SourceStore: store})
	initial := snapshotForTest(t, m, "")
	if _, err := m.Configure(context.Background(), initial.SessionID, initial.Sequence, []byte("https://configs.invalid/old")); err != nil {
		t.Fatal(err)
	}
	before := snapshotForTest(t, m, initial.SessionID)
	_, err := m.Start(context.Background(), before.SessionID, before.Sequence, StartTarget{
		Mode: ProfileIndex, Index: 1, Source: []byte("https://configs.invalid/new"),
	})
	if CodeOf(err) != FailureInvalidArgument {
		t.Fatalf("indexed Start with changed source = %v", err)
	}
	after := snapshotForTest(t, m, initial.SessionID)
	if after.Sequence != before.Sequence || after.Digest != before.Digest ||
		after.SourceURL != before.SourceURL || string(store.value) != before.SourceURL {
		t.Fatalf("rejected indexed Start changed accepted configuration: before %#v, after %#v, stored %q", before, after, store.value)
	}
}

func TestStartRejectsBadChangedSourceWithoutReplacingAcceptedConfiguration(t *testing.T) {
	store := &managerTestSourceStore{}
	m := NewManager(ManagerOptions{Loader: acceptedSourceURLLoader{}, SourceStore: store})
	initial := snapshotForTest(t, m, "")
	_, err := m.Configure(context.Background(), initial.SessionID, initial.Sequence, []byte("https://configs.invalid/old"))
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotForTest(t, m, initial.SessionID)
	_, err = m.Start(context.Background(), before.SessionID, before.Sequence, StartTarget{
		Mode: AutoSelect, Source: []byte("https://configs.invalid/bad"),
	})
	if CodeOf(err) != FailureMalformedConfig {
		t.Fatalf("Start with invalid changed source = %v", err)
	}
	after := snapshotForTest(t, m, initial.SessionID)
	if after.Generation != before.Generation || after.Sequence != before.Sequence ||
		after.SourceURL != before.SourceURL || after.Digest != before.Digest ||
		string(store.value) != before.SourceURL {
		t.Fatalf("invalid changed source modified accepted configuration: before %#v, after %#v, stored %q", before, after, store.value)
	}
}

func TestSourceStoreTracksAcceptedConfigurationSource(t *testing.T) {
	store := &managerTestSourceStore{value: []byte("https://configs.invalid/old")}
	m := NewManager(ManagerOptions{Loader: acceptedSourceURLLoader{}, SourceStore: store})
	initial, err := m.Snapshot(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if initial.Configured || initial.SourceURL != "https://configs.invalid/old" {
		t.Fatalf("restored source snapshot = %#v", initial)
	}
	configured, err := m.Configure(context.Background(), initial.SessionID, initial.Sequence, []byte("https://configs.invalid/new"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(store.value); got != "https://configs.invalid/new" {
		t.Fatalf("stored URL = %q", got)
	}
	if _, err := m.Configure(context.Background(), initial.SessionID, configured.Sequence, []byte("https://configs.invalid/bad")); err == nil {
		t.Fatal("malformed replacement unexpectedly succeeded")
	}
	if got := string(store.value); got != "https://configs.invalid/new" {
		t.Fatalf("failed replacement changed stored URL to %q", got)
	}
	current := snapshotForTest(t, m, initial.SessionID)
	if _, err := m.Configure(context.Background(), initial.SessionID, current.Sequence, fixture(t)); err != nil {
		t.Fatal(err)
	}
	if got := string(store.value); got != "" {
		t.Fatalf("inline configuration retained stale saved URL %q", got)
	}
	inline := snapshotForTest(t, m, initial.SessionID)
	if inline.SourceKind != ConfigSourceInline || inline.SourceURL != "" {
		t.Fatalf("inline session source = %#v", inline)
	}
}

func TestSourceStoreFailureLeavesConfigurationUnchanged(t *testing.T) {
	store := &managerTestSourceStore{saveErr: errors.New("private storage path")}
	m := NewManager(ManagerOptions{Loader: acceptedSourceURLLoader{}, SourceStore: store})
	initial, err := m.Snapshot(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Configure(context.Background(), initial.SessionID, initial.Sequence, []byte("https://configs.invalid/new")); CodeOf(err) != FailurePlatform {
		t.Fatalf("Configure storage failure = %v", err)
	}
	unchanged := snapshotForTest(t, m, initial.SessionID)
	if unchanged.Configured || unchanged.Sequence != initial.Sequence || unchanged.SourceURL != "" {
		t.Fatalf("failed persistence changed session: %#v", unchanged)
	}
}

func TestSourceStoreClearFailureLeavesConfigurationUnchanged(t *testing.T) {
	store := &managerTestSourceStore{value: []byte("https://configs.invalid/old")}
	m := NewManager(ManagerOptions{Loader: acceptedSourceURLLoader{}, SourceStore: store})
	initial := snapshotForTest(t, m, "")
	accepted, err := m.Configure(context.Background(), initial.SessionID, initial.Sequence, []byte("https://configs.invalid/new"))
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotForTest(t, m, initial.SessionID)
	store.clearErr = errors.New("private storage path")
	if _, err := m.Configure(context.Background(), initial.SessionID, accepted.Sequence, fixture(t)); CodeOf(err) != FailurePlatform {
		t.Fatalf("inline Configure storage clear failure = %v", err)
	}
	after := snapshotForTest(t, m, initial.SessionID)
	if after.Sequence != before.Sequence || after.SourceURL != before.SourceURL || after.Digest != before.Digest || !after.Configured {
		t.Fatalf("failed URL clear changed accepted configuration: before=%#v after=%#v", before, after)
	}
	if got := string(store.value); got != "https://configs.invalid/new" {
		t.Fatalf("failed URL clear changed stored URL to %q", got)
	}
}

func TestSourceStoreReadFailureReachesSnapshot(t *testing.T) {
	original := errors.New("source-read-error-sentinel")
	store := &managerTestSourceStore{loadErr: original}
	manager := NewManager(ManagerOptions{SourceStore: store})
	initial := snapshotForTest(t, manager, "")
	if !strings.Contains(initial.SourceError, original.Error()) {
		t.Fatalf("initial snapshot lost source error: %#v", initial)
	}

	attached := NewManager(ManagerOptions{})
	if err := attached.AttachSourceStore(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	snapshot := snapshotForTest(t, attached, "")
	if !strings.Contains(snapshot.SourceError, original.Error()) {
		t.Fatalf("attached snapshot lost source error: %#v", snapshot)
	}
}

type managerTestSourceStore struct {
	value    []byte
	loadErr  error
	saveErr  error
	clearErr error
}

func (s *managerTestSourceStore) Load(context.Context) ([]byte, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	return append([]byte(nil), s.value...), nil
}

func (s *managerTestSourceStore) Save(_ context.Context, value []byte) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.value = append([]byte(nil), value...)
	return nil
}

func (s *managerTestSourceStore) Clear(context.Context) error {
	if s.clearErr != nil {
		return s.clearErr
	}
	s.value = nil
	return nil
}

type acceptedSourceURLLoader struct{}

type failingSourceURLLoader struct{ cause error }

func (l failingSourceURLLoader) Load(context.Context, []byte) (LoadedConfig, error) {
	return LoadedConfig{}, l.cause
}

func TestConfigurationFailuresRetainOriginalCauses(t *testing.T) {
	original := errors.New("source-fetch-error-sentinel")
	manager := NewManager(ManagerOptions{Loader: failingSourceURLLoader{cause: original}})
	initial := snapshotForTest(t, manager, "")
	_, err := manager.Configure(context.Background(), initial.SessionID, initial.Sequence, []byte("https://configs.invalid/private"))
	if CodeOf(err) != FailureInvalidArgument || !errors.Is(err, original) ||
		!strings.Contains(err.Error(), original.Error()) {
		t.Fatalf("configuration fetch lost original cause: %v", err)
	}

	manager = NewManager(ManagerOptions{})
	initial = snapshotForTest(t, manager, "")
	_, err = manager.Configure(context.Background(), initial.SessionID, initial.Sequence, []byte("[[Outline]"))
	if CodeOf(err) != FailureMalformedConfig || errors.Unwrap(err) == nil {
		t.Fatalf("TOML parse lost original cause: %v", err)
	}
}

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
		"[[Outline]]", `Description = "supported-before"`,
		`Server = "198.51.100.20"`, "Port = 443", `Password = "synthetic-password"`,
		"",
		"[[Xray]]", `Description = "legacy-cloak"`,
		"Cloak = true", `Server = "cloak.invalid"`, `Password = "do-not-return"`,
		"",
		"[[TrustTunnel]]", `Description = "supported-after"`, `vpn_mode = "general"`,
		"[TrustTunnel.endpoint]", `hostname = "vpn.invalid"`, `addresses = ["198.51.100.21:443"]`, `username = "synthetic-user"`, `password = "synthetic-password"`,
		"[TrustTunnel.listener.socks]", `address = "127.0.0.1:10808"`,
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
		"multiple Cloak profiles": strings.Join([]string{
			"[[Xray]]", "Cloak = true", `outbounds = [{"address" = "` + syntheticEndpoint + `"}]`,
			"", "[[Outline]]", "Cloak = true", `Server = "` + syntheticURL + `"`, `Password = "` + syntheticCredential + `"`, "Port = 443",
		}, "\n"),
		"all Cloak profiles": strings.Join([]string{
			"[[Outline]]", "Cloak = true", `Server = "` + syntheticURL + `"`, `Password = "` + syntheticCredential + `"`, "Port = 443",
			"", "[[TrustTunnel]]", "Cloak = true",
			"[TrustTunnel.endpoint]", `hostname = "` + syntheticEndpoint + `"`, `password = "` + syntheticCredential + `"`, `addresses = ["` + syntheticEndpoint + `"]`,
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
			if result.Digest != "" || len(result.Profiles) != 0 {
				t.Fatalf("rejected input returned configuration data: %#v", result)
			}
			if _, startErr := startForTest(t, m, id, StartTarget{Mode: AutoSelect}); CodeOf(startErr) != FailureNotConfigured {
				t.Fatalf("start after rejected configure = %v", startErr)
			}
			if runtime.startCalls != 0 {
				t.Fatalf("runtime executed for rejected input: starts=%d", runtime.startCalls)
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

func TestOneOwnerRetainsConfigurationAcrossIdleAndRecovery(t *testing.T) {
	m := NewManager(ManagerOptions{Runtime: &fakeRuntime{readyProfiles: map[int32]bool{0: true}}, Platform: &fakePlatform{}})
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
	if !strings.HasPrefix(profiles[0].Config.OutlineURL, "ss://") {
		t.Fatalf("outline normalization = %#v", profiles[0])
	}
	if len(profiles[1].Config.Xray) == 0 {
		t.Fatalf("xray normalization = %#v", profiles[1].Config)
	}
	if profiles[2].Config.TrustTunnel["endpoint"] == nil {
		t.Fatalf("trusttunnel normalization = %#v", profiles[2].Config)
	}
	if len(profiles[0].ExcludeCIDRs) != 1 || profiles[0].ExcludeCIDRs[0] != "203.0.113.0/24" {
		t.Fatalf("routing inputs = %#v", profiles[0].ExcludeCIDRs)
	}
	result, err := configureForTest(t, m, id, []byte("not = [valid"))
	if err == nil || result.Digest != "" || CodeOf(err) != FailureMalformedConfig {
		t.Fatalf("malformed result=%#v err=%v", result, err)
	}
}

func TestSnapshotCarriesAcceptedMetadata(t *testing.T) {
	m := NewManager(ManagerOptions{})
	before := snapshotForTest(t, m, "")
	configured := acceptAndCheckConfiguration(t, m, before)
	assertInvalidConfigurationDoesNotReplace(t, m, before, configured)
	assertSnapshotCollectionsAreCopied(t, m, before)
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

	initial, err := m.Snapshot(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Configure(context.Background(), initial.SessionID, initial.Sequence, []byte(raw)); err == nil || err.Error() != want {
		t.Fatalf("Configure HTTP URL error = %v, want %q", err, want)
	}
}

func TestStaleRevisionCannotConfigureOrStart(t *testing.T) {
	m := NewManager(ManagerOptions{Runtime: &fakeRuntime{readyProfiles: map[int32]bool{0: true}}, Platform: &fakePlatform{}})
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
	after, err := m.Snapshot(context.Background(), initial.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Sequence != current.Sequence || after.State != StateConfigured || !after.Configured || after.Digest != current.Digest {
		t.Fatalf("stale calls mutated owner: before=%#v after=%#v", current, after)
	}
}

func TestConcurrentConfigureAtSameRevisionOnlyOneMutationWins(t *testing.T) {
	raw := fixture(t)
	for i := 0; i < 50; i++ {
		m := NewManager(ManagerOptions{})
		initial, err := m.Snapshot(context.Background(), "")
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		var firstErr, secondErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, firstErr = m.Configure(context.Background(), initial.SessionID, initial.Sequence, raw)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, secondErr = m.Configure(context.Background(), initial.SessionID, initial.Sequence, raw)
		}()
		close(start)
		wg.Wait()
		if (firstErr == nil) == (secondErr == nil) {
			t.Fatalf("iteration %d: expected exactly one configure to win, first=%v second=%v", i, firstErr, secondErr)
		}
		for _, err := range []error{firstErr, secondErr} {
			if err != nil && CodeOf(err) != FailureConflict {
				t.Fatalf("iteration %d: configure error = %v", i, err)
			}
		}
	}
}

func TestConfigureRejectsMalformedProtocolProfiles(t *testing.T) {
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

func TestAutoSelectionUsesFirstWorkingProfileInSourceOrder(t *testing.T) {
	r := &fakeRuntime{readyProfiles: map[int32]bool{0: true, 1: true, 2: true, 3: true}}
	platform := &eventPlatform{events: make(chan StateChange, 32)}
	m := NewManager(ManagerOptions{Runtime: r, Platform: platform})
	id := configured(t, m)
	start, err := startForTest(t, m, id, StartTarget{Mode: AutoSelect})
	if err != nil {
		t.Fatal(err)
	}
	s := waitState(t, m, id, StateConnected)
	if s.ActiveProfile == nil || s.ActiveProfile.Index != 0 {
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

func TestProfileIndexStartFailureDoesNotTryAnotherProfile(t *testing.T) {
	order := &recordedOrder{}
	runtime := &orderedRuntime{
		order:       order,
		startErrors: map[int32]error{0: errors.New("explicit candidate failed")},
	}
	platform := &orderedCandidatePlatform{order: order, events: make(chan StateChange, 32)}
	m := NewManager(ManagerOptions{Runtime: runtime, Platform: platform})
	id := configured(t, m)
	if _, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex, Index: 0}); err != nil {
		t.Fatal(err)
	}
	snapshot := waitState(t, m, id, StateFailed)
	if snapshot.LastFailure != FailureRuntime || !strings.Contains(snapshot.LastFailureMessage, "explicit candidate failed") {
		t.Fatalf("explicit profile failure=%#v", snapshot)
	}
	want := []string{"prepare-1", "start-0", "release-1"}
	if got := order.itemsCopy(); !sameStrings(got, want) {
		t.Fatalf("explicit candidate ordering=%v, want=%v", got, want)
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
	if snapshot.CleanupComplete || snapshot.LastFailure != FailureCleanup {
		t.Fatalf("cleanup snapshot=%#v", snapshot)
	}
	if _, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex, Index: 0}); CodeOf(err) != FailureConflict {
		t.Fatalf("restart error=%v, want %s", err, FailureConflict)
	}
	if _, err := configureForTest(t, m, id, fixture(t)); CodeOf(err) != FailureConflict {
		t.Fatalf("configure after cleanup failure error=%v, want %s", err, FailureConflict)
	}
}

func TestStopAcknowledgesAlreadyCleanedTerminalGeneration(t *testing.T) {
	failures := make(chan error, 1)
	runtime := &monitoringRuntime{failures: failures, stopped: make(chan uint64, 2)}
	platform := &eventPlatform{events: make(chan StateChange, 32)}
	m := NewManager(ManagerOptions{Runtime: runtime, Platform: platform})
	id := configured(t, m)
	start, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex, Index: 0})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, id, StateConnected)
	failures <- errors.New("connected health probe: endpoint unavailable")
	snapshot := waitState(t, m, id, StateFailed)
	if !snapshot.CleanupComplete || snapshot.LastFailure != FailureRuntime || !strings.Contains(snapshot.LastFailureMessage, "endpoint unavailable") {
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
		Runtime:  &fakeRuntime{readyProfiles: map[int32]bool{0: true}},
		Platform: errorWithLeasePlatform{prepareErr: errors.New("prepare failed"), releaseErr: want},
	})
	id := configured(t, m)
	if _, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex, Index: 0}); err != nil {
		t.Fatal(err)
	}
	snapshot := waitState(t, m, id, StateFailed)
	if snapshot.CleanupComplete || snapshot.LastFailure != FailureCleanup {
		t.Fatalf("cleanup snapshot=%#v", snapshot)
	}
}

func TestAutoSelectStartLeaseCleanupPreservesBothCauses(t *testing.T) {
	candidateErr := errors.New("prior profile startup failed")
	prepareErr := errors.New("candidate platform setup failed")
	releaseErr := errors.New("candidate platform rollback failed")
	m := NewManager(ManagerOptions{
		Runtime:  startFailureRuntime{err: candidateErr},
		Platform: &failingSecondCandidatePlatform{prepareErr: prepareErr, releaseErr: releaseErr},
	})
	id := configured(t, m)
	if _, err := startForTest(t, m, id, StartTarget{Mode: AutoSelect}); err != nil {
		t.Fatal(err)
	}
	snapshot := waitState(t, m, id, StateFailed)
	for _, cause := range []string{candidateErr.Error(), prepareErr.Error(), releaseErr.Error()} {
		if !strings.Contains(snapshot.LastFailureMessage, cause) {
			t.Fatalf("failure snapshot lost %q: %#v", cause, snapshot)
		}
	}
	if snapshot.LastFailure != FailureCleanup || snapshot.CleanupComplete {
		t.Fatalf("candidate platform failure snapshot=%#v", snapshot)
	}
	if _, err := m.Start(context.Background(), id, snapshot.Sequence, StartTarget{Mode: AutoSelect}); CodeOf(err) != FailureConflict {
		t.Fatalf("restart after candidate platform cleanup failure error=%v", err)
	}
}

func TestAutoSelectStartCauseSurvivesInSnapshot(t *testing.T) {
	const exact = "endpoint probe returned HTTP 502: upstream unavailable"
	m := NewManager(ManagerOptions{
		Runtime:  startFailureRuntime{err: errors.New(exact)},
		Platform: &fakePlatform{},
	})
	id := configured(t, m)
	if _, err := startForTest(t, m, id, StartTarget{Mode: AutoSelect}); err != nil {
		t.Fatal(err)
	}
	snapshot := waitState(t, m, id, StateFailed)
	if snapshot.LastFailure != FailureProbe || !strings.Contains(snapshot.LastFailureMessage, exact) {
		t.Fatalf("probe failure snapshot lost endpoint cause: %#v", snapshot)
	}
}

type cleanupFailStartRuntime struct {
	*fakeRuntime
	startCalls int
}

func (r *cleanupFailStartRuntime) Start(context.Context, SessionRef, RuntimeProfile) (RuntimeLease, error) {
	r.startCalls++
	if r.startCalls == 1 {
		return nil, &CleanupFailure{Err: errors.New("runtime start rollback failed")}
	}
	return fakeRuntimeLease{}, nil
}

func TestAutoSelectionStopsAfterStartRollbackFailure(t *testing.T) {
	runtime := &cleanupFailStartRuntime{fakeRuntime: &fakeRuntime{readyProfiles: map[int32]bool{0: true}}}
	m := NewManager(ManagerOptions{Runtime: runtime, Platform: &fakePlatform{}})
	id := configured(t, m)
	if _, err := startForTest(t, m, id, StartTarget{Mode: AutoSelect}); err != nil {
		t.Fatal(err)
	}
	snapshot := waitState(t, m, id, StateFailed)
	if snapshot.LastFailure != FailureCleanup || runtime.startCalls != 1 {
		t.Fatalf("failure=%s start calls=%d", snapshot.LastFailure, runtime.startCalls)
	}
	if _, err := m.Start(context.Background(), id, snapshot.Sequence, StartTarget{Mode: AutoSelect}); CodeOf(err) != FailureConflict {
		t.Fatalf("restart after cleanup failure error=%v", err)
	}
}

func TestSnapshotPrimaryActionTracksStartStopAndCleanup(t *testing.T) {
	m := NewManager(ManagerOptions{Runtime: &fakeRuntime{readyProfiles: map[int32]bool{0: true}}, Platform: &fakePlatform{}})
	id, err := currentSessionForTest(t, m)
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshotForTest(t, m, id).PrimaryAction; got != "START" {
		t.Fatalf("idle action=%q", got)
	}
	_, err = configureForTest(t, m, id, fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	start, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex, Index: 0})
	if err != nil {
		t.Fatal(err)
	}
	if got := waitState(t, m, id, StateConnected).PrimaryAction; got != "STOP" {
		t.Fatalf("connected action=%q", got)
	}
	if _, err := m.Stop(context.Background(), id, start.Generation); err != nil {
		t.Fatal(err)
	}
	if got := waitState(t, m, id, StateIdle).PrimaryAction; got != "START" {
		t.Fatalf("clean idle action=%q", got)
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
	if snapshot.CleanupComplete || snapshot.LastFailure != FailureCleanup {
		t.Fatalf("cleanup snapshot=%#v", snapshot)
	}
}

func TestRuntimeStartRollbackFailureBlocksRestartWithoutReturnedLeaseError(t *testing.T) {
	m := NewManager(ManagerOptions{
		Runtime: errorWithLeaseRuntime{
			startErr: &CleanupFailure{Err: errors.New("runtime rollback failed")},
		},
		Platform: &fakePlatform{},
	})
	id := configured(t, m)
	if _, err := startForTest(t, m, id, StartTarget{Mode: ProfileIndex, Index: 0}); err != nil {
		t.Fatal(err)
	}
	snapshot := waitState(t, m, id, StateFailed)
	if snapshot.LastFailure != FailureCleanup || snapshot.CleanupComplete {
		t.Fatalf("runtime rollback snapshot=%#v", snapshot)
	}
	if _, err := m.Start(context.Background(), id, snapshot.Sequence, StartTarget{Mode: ProfileIndex, Index: 0}); CodeOf(err) != FailureConflict {
		t.Fatalf("restart after runtime rollback failure error=%v", err)
	}
}

func TestAutoSelectionRetainsFirstSuccessfulRuntimeAndPlatformLeases(t *testing.T) {
	order := &recordedOrder{}
	runtime := &orderedRuntime{
		order: order,
		startErrors: map[int32]error{
			0: errors.New("first profile is not ready"),
			1: errors.New("second profile is not ready"),
		},
	}
	platform := &orderedCandidatePlatform{order: order, events: make(chan StateChange, 32)}
	m := NewManager(ManagerOptions{Runtime: runtime, Platform: platform})
	id := configured(t, m)
	start, err := startForTest(t, m, id, StartTarget{Mode: AutoSelect})
	if err != nil {
		t.Fatal(err)
	}
	connected := waitState(t, m, id, StateConnected)
	if connected.ActiveProfile == nil || connected.ActiveProfile.Index != 2 {
		t.Fatalf("active profile = %#v, want first ready index 2", connected.ActiveProfile)
	}
	waitForEvent(t, platform.events, start.Generation, StateConnected)
	wantPrefix := make([]string, 0, 10)
	wantPrefix = append(wantPrefix,
		"prepare-1", "start-0", "release-1",
		"prepare-2", "start-1", "release-2",
		"prepare-3", "start-2",
	)
	if got := order.itemsCopy(); !sameStrings(got, wantPrefix) {
		t.Fatalf("candidate ordering=%v, want=%v", got, wantPrefix)
	}
	if _, err := m.Stop(context.Background(), id, start.Generation); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, id, StateIdle)
	wantStopped := append(wantPrefix, "stop-2", "release-3")
	if got := order.itemsCopy(); !sameStrings(got, wantStopped) {
		t.Fatalf("winner cleanup ordering=%v, want=%v", got, wantStopped)
	}
}

func TestAutoSelectionReleasesCandidateLeaseWhenCanceled(t *testing.T) {
	entered := make(chan struct{}, 1)
	released := make(chan struct{}, 1)
	runtime := &fakeRuntime{readyProfiles: map[int32]bool{0: true}, blockStart: make(chan struct{}), startEntered: entered}
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
		t.Fatal("canceled candidate did not release its platform lease")
	}
	waitState(t, m, id, StateIdle)
}

func TestAutoSelectionReportsPlatformPreparationFailure(t *testing.T) {
	platform := failingPreparePlatform{events: make(chan StateChange, 8)}
	m := NewManager(ManagerOptions{Runtime: &fakeRuntime{readyProfiles: map[int32]bool{0: true}}, Platform: platform})
	id := configured(t, m)
	if _, err := startForTest(t, m, id, StartTarget{Mode: AutoSelect}); err != nil {
		t.Fatal(err)
	}
	event := waitForEvent(t, platform.events, 1, StateFailed)
	if event.Failure != FailurePlatform {
		t.Fatalf("failure=%s, want %s", event.Failure, FailurePlatform)
	}
}

func TestStopDuringCandidateStartPreventsLateConnectedAndAllowsRestartAfterCleanup(t *testing.T) {
	r := &fakeRuntime{readyProfiles: map[int32]bool{0: true}, blockStart: make(chan struct{}), startEntered: make(chan struct{}, 1)}
	p := &fakePlatform{}
	m := NewManager(ManagerOptions{Runtime: r, Platform: p})
	id := configured(t, m)
	first, err := startForTest(t, m, id, StartTarget{Mode: AutoSelect})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.startEntered:
	case <-time.After(time.Second):
		t.Fatal("candidate Start did not begin")
	}
	if _, stopErr := m.Stop(context.Background(), id, first.Generation); stopErr != nil {
		t.Fatal(stopErr)
	}
	waitState(t, m, id, StateIdle)
	close(r.blockStart)
	time.Sleep(10 * time.Millisecond) // permits the deliberately stale Start completion to return
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
	r := &fakeRuntime{readyProfiles: map[int32]bool{0: true}, stopHook: func() { orderMu.Lock(); order = append(order, "runtime"); orderMu.Unlock() }}
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
	failures := make(chan error, 1)
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
	failures <- errors.New("synthetic health check failure")
	waitForEvent(t, platform.events, first.Generation, StateStopping)
	waitForEvent(t, platform.events, first.Generation, StateIdle)
	if stopped := <-runtime.stopped; stopped != first.Generation {
		t.Fatalf("cleanup stopped generation %d, want %d", stopped, first.Generation)
	}
	second := waitForEvent(t, platform.events, first.Generation+1, StateProbing)
	if second.Generation != first.Generation+1 {
		t.Fatalf("failover generation = %d, want %d", second.Generation, first.Generation+1)
	}
	if second.Failure != FailureRuntime {
		t.Fatalf("recovery probe event lost health failure code: %#v", second)
	}
	waitForEvent(t, platform.events, first.Generation+1, StateConnected)
}

func TestRuntimeOwnedHealthFailureDoesNotReplaceExplicitProfile(t *testing.T) {
	failures := make(chan error, 1)
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
	failures <- errors.New("synthetic health check failure")
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
	if snapshot.CleanupComplete || snapshot.LastFailure != FailureCleanup {
		t.Fatalf("cleanup snapshot=%#v", snapshot)
	}
}

func TestStopRetainsLateRuntimeRollbackFailureWithoutLease(t *testing.T) {
	entered := make(chan struct{})
	releaseStart := make(chan struct{})
	m := NewManager(ManagerOptions{
		Runtime:  blockingRollbackRuntime{entered: entered, release: releaseStart},
		Platform: &fakePlatform{},
	})
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
	snapshot := waitState(t, m, id, StateFailed)
	if snapshot.LastFailure != FailureCleanup || snapshot.CleanupComplete {
		t.Fatalf("late rollback snapshot=%#v", snapshot)
	}
	if _, err := m.Start(context.Background(), id, snapshot.Sequence, StartTarget{Mode: ProfileIndex, Index: 0}); CodeOf(err) != FailureConflict {
		t.Fatalf("restart after late rollback failure=%v", err)
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
	m := NewManager(ManagerOptions{Runtime: &fakeRuntime{readyProfiles: map[int32]bool{0: true}}, Platform: platform})
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
	if snapshot.CleanupComplete || snapshot.LastFailure != FailureCleanup {
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
	readyProfiles map[int32]bool
	blockStart    chan struct{}
	startEntered  chan struct{}
	stopHook      func()
}

type startErrorRuntime struct{ err error }

func (r *startErrorRuntime) Start(context.Context, SessionRef, RuntimeProfile) (RuntimeLease, error) {
	return nil, r.err
}

type startFailureRuntime struct{ err error }

func (r startFailureRuntime) Start(context.Context, SessionRef, RuntimeProfile) (RuntimeLease, error) {
	return nil, r.err
}

type countingRuntime struct{ startCalls int }

func (r *countingRuntime) Start(context.Context, SessionRef, RuntimeProfile) (RuntimeLease, error) {
	r.startCalls++
	return fakeRuntimeLease{}, nil
}

func (r *fakeRuntime) Start(ctx context.Context, _ SessionRef, profile RuntimeProfile) (RuntimeLease, error) {
	if r.startEntered != nil {
		select {
		case r.startEntered <- struct{}{}:
		default:
			// A notification is already pending.
		}
	}
	if r.blockStart != nil {
		select {
		case <-r.blockStart:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if !r.readyProfiles[profile.Summary.Index] {
		return nil, errors.New("profile did not become ready")
	}
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

func (r *cleanupErrorRuntime) Start(context.Context, SessionRef, RuntimeProfile) (RuntimeLease, error) {
	return cleanupErrorLease{err: r.err}, nil
}

type cleanupErrorLease struct{ err error }

func (l cleanupErrorLease) Stop(context.Context) error { return l.err }

type errorWithLeaseRuntime struct {
	startErr error
	stopErr  error
}

func (r errorWithLeaseRuntime) Start(context.Context, SessionRef, RuntimeProfile) (RuntimeLease, error) {
	return cleanupErrorLease{err: r.stopErr}, r.startErr
}

type monitoringRuntime struct {
	failures <-chan error
	stopped  chan uint64
}

func (r *monitoringRuntime) Start(_ context.Context, ref SessionRef, _ RuntimeProfile) (RuntimeLease, error) {
	return monitoringRuntimeLease{generation: ref.Generation, failures: r.failures, stopped: r.stopped}, nil
}

type monitoringRuntimeLease struct {
	generation uint64
	failures   <-chan error
	stopped    chan uint64
}

func (l monitoringRuntimeLease) Stop(context.Context) error   { l.stopped <- l.generation; return nil }
func (l monitoringRuntimeLease) HealthFailures() <-chan error { return l.failures }

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

type failingSecondCandidatePlatform struct {
	prepareCalls int
	prepareErr   error
	releaseErr   error
}

func (p *failingSecondCandidatePlatform) PrepareTunnel(context.Context, SessionRef) (PlatformLease, error) {
	p.prepareCalls++
	if p.prepareCalls == 1 {
		return noopLease{}, nil
	}
	return cleanupErrorPlatformLease{err: p.releaseErr}, p.prepareErr
}
func (*failingSecondCandidatePlatform) ProtectSocket(context.Context, SessionRef, int) error {
	return nil
}
func (*failingSecondCandidatePlatform) PublishState(context.Context, StateChange) {}

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

type orderedRuntime struct {
	order       *recordedOrder
	startErrors map[int32]error
}

func (r *orderedRuntime) Start(_ context.Context, _ SessionRef, profile RuntimeProfile) (RuntimeLease, error) {
	r.order.add(fmt.Sprintf("start-%d", profile.Summary.Index))
	if err := r.startErrors[profile.Summary.Index]; err != nil {
		return nil, err
	}
	index := profile.Summary.Index
	return fakeRuntimeLease{stop: func() { r.order.add(fmt.Sprintf("stop-%d", index)) }}, nil
}

type orderedCandidatePlatform struct {
	order    *recordedOrder
	events   chan StateChange
	prepared int
}

func (p *orderedCandidatePlatform) PrepareTunnel(_ context.Context, _ SessionRef) (PlatformLease, error) {
	p.prepared++
	index := p.prepared
	p.order.add(fmt.Sprintf("prepare-%d", index))
	return fakePlatformLease{release: func() { p.order.add(fmt.Sprintf("release-%d", index)) }}, nil
}
func (*orderedCandidatePlatform) ProtectSocket(context.Context, SessionRef, int) error { return nil }
func (p *orderedCandidatePlatform) PublishState(_ context.Context, event StateChange) {
	p.events <- event
}

type releaseSignalPlatform struct{ released chan<- struct{} }

func (p *releaseSignalPlatform) PrepareTunnel(context.Context, SessionRef) (PlatformLease, error) {
	return fakePlatformLease{release: func() { p.released <- struct{}{} }}, nil
}
func (*releaseSignalPlatform) ProtectSocket(context.Context, SessionRef, int) error { return nil }
func (*releaseSignalPlatform) PublishState(context.Context, StateChange)            {}

type failingPreparePlatform struct{ events chan StateChange }

func (f failingPreparePlatform) PrepareTunnel(context.Context, SessionRef) (PlatformLease, error) {
	return nil, errors.New("prepare candidate")
}
func (f failingPreparePlatform) ProtectSocket(context.Context, SessionRef, int) error { return nil }
func (f failingPreparePlatform) PublishState(_ context.Context, event StateChange) {
	f.events <- event
}

type blockingStartRuntime struct {
	entered chan<- struct{}
	release <-chan struct{}
	stopped chan<- struct{}
	err     error
}

type blockingRollbackRuntime struct {
	entered chan<- struct{}
	release <-chan struct{}
}

func (r blockingRollbackRuntime) Start(context.Context, SessionRef, RuntimeProfile) (RuntimeLease, error) {
	r.entered <- struct{}{}
	<-r.release
	return nil, &CleanupFailure{Err: errors.New("late runtime rollback failed")}
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
