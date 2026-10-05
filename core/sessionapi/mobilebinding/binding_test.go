package mobilebinding

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"core/sessionapi"
	"core/sessionapi/wire"
)

const syntheticConfig = `[[Outline]]
Server = "vpn.example.invalid"
Port = 443
Password = "super-secret-token"
`

func TestFailureEnvelopeRetainsOriginalCause(t *testing.T) {
	original := errors.New("native-adapter-error-sentinel")
	response := failed(&sessionapi.Error{
		Code: sessionapi.FailurePlatform, Message: "platform preparation failed", Cause: original,
	})
	var decoded struct {
		Error wire.Failure `json:"error"`
	}
	if err := json.Unmarshal([]byte(response), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Error.Code != string(sessionapi.FailurePlatform) ||
		!strings.Contains(decoded.Error.Message, original.Error()) {
		t.Fatalf("failure envelope lost cause: %s", response)
	}
}

func TestJSONEnvelopeUsesStableKeys(t *testing.T) {
	binding := NewForDesktop(sessionapi.NewManager(sessionapi.ManagerOptions{}))
	initial := binding.Snapshot("")
	if strings.Contains(initial, "SessionID") || !strings.Contains(initial, `"session_id"`) {
		t.Fatalf("snapshot did not use stable snake_case: %s", initial)
	}
	sessionID := jsonSessionID(t, initial)
	configured := binding.Configure(sessionID, int64Field(t, initial, "sequence"), []byte(syntheticConfig))
	for _, field := range []string{`"sequence"`, `"source_kind":"INLINE"`, `"profiles"`} {
		if !strings.Contains(configured, field) || strings.Contains(configured, `"Profiles"`) {
			t.Fatalf("configure response did not use stable complete DTO keys: %s", configured)
		}
	}
	if strings.Contains(configured, `"warnings"`) {
		t.Fatalf("configure response retained the unused warnings field: %s", configured)
	}
}

func TestCallJSONReturnsAcceptedURLInSharedSnapshot(t *testing.T) {
	binding := NewForDesktop(sessionapi.NewManager(sessionapi.ManagerOptions{Loader: acceptedURLLoader{}}))
	initial := binding.Snapshot("")
	sessionID := jsonSessionID(t, initial)
	sequence := int64Field(t, initial, "sequence")
	configured := binding.CallJSON(context.Background(), "Configure", json.RawMessage(
		`{"session_id":"`+sessionID+`","expected_sequence":`+strconv.FormatInt(sequence, 10)+`,"source":"https://configs.invalid/current"}`,
	))
	if !strings.Contains(configured, `"source_kind":"URL"`) {
		t.Fatalf("Configure response = %s", configured)
	}
	snapshot := binding.CallJSON(context.Background(), "Snapshot", json.RawMessage(`{"session_id":"`+sessionID+`"}`))
	if !strings.Contains(snapshot, `"source_url":"https://configs.invalid/current"`) {
		t.Fatalf("Snapshot did not return the accepted source URL: %s", snapshot)
	}
}

func TestCallJSONWithConfigurationUsesSharedDispatcherAndSeparateSecret(t *testing.T) {
	binding := NewForDesktop(sessionapi.NewManager(sessionapi.ManagerOptions{}))
	initial := binding.Snapshot("")
	sessionID := jsonSessionID(t, initial)
	sequence := int64Field(t, initial, "sequence")
	params := json.RawMessage(`{"session_id":"` + sessionID + `","expected_sequence":` + strconv.FormatInt(sequence, 10) + `}`)
	configured := binding.CallJSONWithConfiguration(
		context.Background(), "Configure", params, []byte(syntheticConfig), true,
	)
	if !strings.Contains(configured, `"ok":true`) || !strings.Contains(configured, `"source_kind":"INLINE"`) {
		t.Fatalf("Configure did not dispatch with its separate configuration: %s", configured)
	}
	if strings.Contains(configured, "super-secret-token") {
		t.Fatalf("configuration secret leaked in response: %s", configured)
	}

	snapshot := binding.CallJSON(context.Background(), "Snapshot", json.RawMessage(`{"session_id":"`+sessionID+`"}`))
	if !strings.Contains(snapshot, `"ok":true`) {
		t.Fatalf("Snapshot did not dispatch through the shared method boundary: %s", snapshot)
	}
	removed := binding.CallJSON(context.Background(), "Reset", json.RawMessage(`{}`))
	if !strings.Contains(removed, `"code":"INVALID_ARGUMENT"`) {
		t.Fatalf("removed Reset command was accepted: %s", removed)
	}
}

func TestCallJSONWithEmptyMailboxLeavesConfigurationValidationToGo(t *testing.T) {
	binding := NewForDesktop(sessionapi.NewManager(sessionapi.ManagerOptions{}))
	initial := binding.Snapshot("")
	params := json.RawMessage(`{"session_id":"` + jsonSessionID(t, initial) + `","expected_sequence":` + strconv.FormatInt(int64Field(t, initial, "sequence"), 10) + `}`)
	response := binding.CallJSONWithConfiguration(
		context.Background(), "Configure", params, []byte{}, true,
	)
	if !strings.Contains(response, `"code":"INVALID_ARGUMENT"`) ||
		!strings.Contains(response, `"message":"configuration source is empty"`) {
		t.Fatalf("empty config was not rejected by the shared Go configuration path: %s", response)
	}
}

func TestStartJSONAcceptsSourceInRequestOrSeparateMailbox(t *testing.T) {
	for _, separate := range []bool{false, true} {
		manager := sessionapi.NewManager(sessionapi.ManagerOptions{
			Loader: acceptedURLLoader{}, Runtime: &blockingRuntime{}, Platform: &recordingPlatform{},
		})
		binding := NewForDesktop(manager)
		initial := binding.Snapshot("")
		sessionID := jsonSessionID(t, initial)
		params := `{"session_id":"` + sessionID + `","expected_sequence":` +
			strconv.FormatInt(int64Field(t, initial, "sequence"), 10) +
			`,"mode":"AUTO_SELECT","index":0`
		const source = "https://configs.invalid/new"
		var started string
		if separate {
			started = binding.CallJSONWithConfiguration(
				context.Background(), "Start", json.RawMessage(params+`}`), []byte(source), true,
			)
		} else {
			started = binding.CallJSON(context.Background(), "Start", json.RawMessage(params+`,"source":"`+source+`"}`))
		}
		if !strings.Contains(started, `"ok":true`) {
			t.Fatalf("Start separate=%t: %s", separate, started)
		}
		snapshot := binding.Snapshot(sessionID)
		if !strings.Contains(snapshot, `"source_url":"`+source+`"`) {
			t.Fatalf("Start separate=%t did not accept source: %s", separate, snapshot)
		}
		_ = binding.Stop(sessionID, int64Field(t, started, "generation"))
	}
}

func TestStartWithEmptyMailboxRejectsChangedSource(t *testing.T) {
	binding := NewForDesktop(sessionapi.NewManager(sessionapi.ManagerOptions{}))
	initial := binding.Snapshot("")
	params := json.RawMessage(`{"session_id":"` + jsonSessionID(t, initial) + `","expected_sequence":` +
		strconv.FormatInt(int64Field(t, initial, "sequence"), 10) + `,"mode":"AUTO_SELECT","index":0}`)
	response := binding.CallJSONWithConfiguration(context.Background(), "Start", params, nil, true)
	if !strings.Contains(response, `"code":"INVALID_ARGUMENT"`) ||
		!strings.Contains(response, `"message":"configuration source is empty"`) {
		t.Fatalf("empty Start source was not rejected by Go: %s", response)
	}
}

type acceptedURLLoader struct{}

func (acceptedURLLoader) Load(_ context.Context, source []byte) (sessionapi.LoadedConfig, error) {
	if !strings.HasPrefix(string(source), "https://") {
		return sessionapi.LoadedConfig{Raw: append([]byte(nil), source...), Kind: sessionapi.ConfigSourceInline}, nil
	}
	return sessionapi.LoadedConfig{Raw: []byte(syntheticConfig), Kind: sessionapi.ConfigSourceURL, SourceURL: string(source)}, nil
}

func TestSnapshotDTOAlwaysRoundTripsRecoveringFlag(t *testing.T) {
	for _, recovering := range []bool{false, true} {
		encoded, err := json.Marshal(wire.SnapshotFrom(sessionapi.SnapshotResult{Recovering: recovering}))
		if err != nil {
			t.Fatalf("marshal snapshot: %v", err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatalf("unmarshal snapshot: %v", err)
		}
		raw, ok := fields["recovering"]
		if !ok {
			t.Fatalf("snapshot omitted recovering=false/true field: %s", encoded)
		}
		var decoded bool
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("decode recovering field: %v", err)
		}
		if decoded != recovering {
			t.Fatalf("recovering round-trip = %t, want %t", decoded, recovering)
		}
	}
}

func TestSnapshotCarriesAcceptedConfiguration(t *testing.T) {
	binding := NewForDesktop(sessionapi.NewManager(sessionapi.ManagerOptions{}))
	initial := binding.Snapshot("")
	sessionID := jsonSessionID(t, initial)
	configured := binding.Configure(sessionID, int64Field(t, initial, "sequence"), []byte(syntheticConfig))
	if strings.Contains(configured, "super-secret-token") {
		t.Fatalf("configuration bytes leaked in Configure result: %s", configured)
	}
	snapshot := binding.Snapshot(sessionID)
	for _, field := range []string{`"sequence":`, `"digest":`, `"source_kind":"INLINE"`, `"profiles":[`, `"recovering":false`} {
		if !strings.Contains(snapshot, field) {
			t.Fatalf("snapshot missing %s: %s", field, snapshot)
		}
	}
	if strings.Contains(snapshot, `"warnings"`) {
		t.Fatalf("snapshot retained the unused warnings field: %s", snapshot)
	}
	if strings.Contains(snapshot, "super-secret-token") {
		t.Fatalf("configuration bytes leaked in Snapshot: %s", snapshot)
	}
}

type fixedSnapshotManager struct {
	snapshot sessionapi.SnapshotResult
}

func (m fixedSnapshotManager) Configure(context.Context, string, uint64, []byte) (sessionapi.ConfigureResult, error) {
	return sessionapi.ConfigureResult{}, nil
}
func (m fixedSnapshotManager) Start(context.Context, string, uint64, sessionapi.StartTarget) (sessionapi.StartResult, error) {
	return sessionapi.StartResult{}, nil
}
func (m fixedSnapshotManager) Stop(context.Context, string, uint64) (sessionapi.StopResult, error) {
	return sessionapi.StopResult{}, nil
}
func (m fixedSnapshotManager) Snapshot(context.Context, string) (sessionapi.SnapshotResult, error) {
	return m.snapshot, nil
}

func TestCallJSONSnapshotPreservesActiveAndPendingSelectionIdentity(t *testing.T) {
	binding := &Binding{manager: fixedSnapshotManager{snapshot: sessionapi.SnapshotResult{
		SessionID: "session", Sequence: 9, Generation: 4, State: sessionapi.StateStopping,
		Configured: true, Digest: "loaded-digest", ActiveDigest: "active-digest",
		ActiveMode: sessionapi.ProfileIndex, ActiveIndex: 1, CanSwitch: false,
		PendingTarget: &sessionapi.Selection{Digest: "target-digest", Mode: sessionapi.ProfileIndex, Index: 2},
		PrimaryAction: "STOP",
	}}}
	response := binding.CallJSON(context.Background(), "Snapshot", json.RawMessage(`{"session_id":"session"}`))
	var decoded wire.Response[wire.Snapshot]
	if err := json.Unmarshal([]byte(response), &decoded); err != nil {
		t.Fatalf("decode Snapshot: %v (%s)", err, response)
	}
	if !decoded.OK || decoded.Result.ActiveDigest != "active-digest" || decoded.Result.Digest != "loaded-digest" ||
		decoded.Result.ActiveMode != string(sessionapi.ProfileIndex) || decoded.Result.ActiveIndex != 1 ||
		decoded.Result.PendingTarget == nil || decoded.Result.PendingTarget.Digest != "target-digest" ||
		decoded.Result.PendingTarget.Mode != string(sessionapi.ProfileIndex) || decoded.Result.PendingTarget.Index != 2 ||
		decoded.Result.CanSwitch || decoded.Result.PrimaryAction != "STOP" {
		t.Fatalf("native Snapshot JSON lost selection identity or action state: %#v", decoded.Result)
	}
}

type gatedSwitchRuntime struct {
	starts      chan sessionapi.RuntimeProfile
	stopEntered chan struct{}
	releaseStop chan struct{}
	stopOnce    sync.Once
}

func (r *gatedSwitchRuntime) Start(_ context.Context, _ sessionapi.SessionRef, profile sessionapi.RuntimeProfile) (sessionapi.RuntimeLease, error) {
	r.starts <- profile
	return gatedSwitchLease{runtime: r}, nil
}

type gatedSwitchLease struct{ runtime *gatedSwitchRuntime }

func (l gatedSwitchLease) Stop(context.Context) error {
	l.runtime.stopOnce.Do(func() { close(l.runtime.stopEntered) })
	<-l.runtime.releaseStop
	return nil
}

func decodeBindingSnapshot(t *testing.T, binding *Binding, sessionID string) wire.Snapshot {
	t.Helper()
	var response wire.Response[wire.Snapshot]
	if err := json.Unmarshal([]byte(binding.Snapshot(sessionID)), &response); err != nil {
		t.Fatalf("decode binding Snapshot: %v", err)
	}
	if !response.OK {
		t.Fatalf("binding Snapshot failed: %#v", response.Error)
	}
	return response.Result
}

func waitBindingSnapshot(t *testing.T, binding *Binding, sessionID, state string) wire.Snapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := decodeBindingSnapshot(t, binding, sessionID)
		if snapshot.State == state {
			return snapshot
		}
		time.Sleep(time.Millisecond)
	}
	snapshot := decodeBindingSnapshot(t, binding, sessionID)
	t.Fatalf("binding Snapshot did not reach %s: %#v", state, snapshot)
	return wire.Snapshot{}
}

func TestReopenedSnapshotRetainsManualAndPendingSelectionState(t *testing.T) {
	const originalConfig = `[[Outline]]
Description = "first profile"
Server = "first.example.invalid"
Port = 443
Password = "first-synthetic-password"

[[Outline]]
Description = "active manual profile"
Server = "second.example.invalid"
Port = 443
Password = "second-synthetic-password"
`
	runtime := &gatedSwitchRuntime{
		starts:      make(chan sessionapi.RuntimeProfile, 4),
		stopEntered: make(chan struct{}),
		releaseStop: make(chan struct{}),
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(runtime.releaseStop) }) }
	manager := sessionapi.NewManager(sessionapi.ManagerOptions{Runtime: runtime})
	binding := NewForDesktop(manager)
	t.Cleanup(func() {
		release()
		_ = binding.StopAndWait(context.Background())
	})
	initial := decodeBindingSnapshot(t, binding, "")
	connected := prepareManualSelectionForReopen(t, binding, runtime, initial, originalConfig)
	reopened, loaded := reopenWithReplacementInventory(t, manager, binding, connected, initial.SessionID)
	startReplacementAndCheckPending(t, manager, runtime, reopened, initial.SessionID, connected, loaded)
	shutdownReopenedBinding(t, reopened, runtime, release, initial.SessionID, connected)
}

func prepareManualSelectionForReopen(t *testing.T, binding *Binding, runtime *gatedSwitchRuntime, initial wire.Snapshot, originalConfig string) wire.Snapshot {
	t.Helper()
	configured := binding.Configure(initial.SessionID, int64(initial.Sequence), []byte(originalConfig))
	var configuredResponse wire.Response[wire.Configuration]
	if err := json.Unmarshal([]byte(configured), &configuredResponse); err != nil || !configuredResponse.OK {
		t.Fatalf("configure original inventory: response=%s error=%v", configured, err)
	}
	started := binding.StartSelection(initial.SessionID, int64(configuredResponse.Result.Sequence), string(sessionapi.ProfileIndex), 1, configuredResponse.Result.Digest, false)
	var startResponse wire.Response[wire.Generation]
	if err := json.Unmarshal([]byte(started), &startResponse); err != nil || !startResponse.OK {
		t.Fatalf("start manual profile: response=%s error=%v", started, err)
	}
	connected := waitBindingSnapshot(t, binding, initial.SessionID, string(sessionapi.StateConnected))
	if profile := <-runtime.starts; profile.Summary.Description != "active manual profile" {
		t.Fatalf("started profile = %#v", profile.Summary)
	}
	if connected.ActiveDigest != configuredResponse.Result.Digest || connected.ActiveMode != string(sessionapi.ProfileIndex) ||
		connected.ActiveIndex != 1 || connected.ActiveProfile == nil || connected.ActiveProfile.Description != "active manual profile" {
		t.Fatalf("connected Snapshot lost manual selection identity: %#v", connected)
	}
	return connected
}

func reopenWithReplacementInventory(t *testing.T, manager *sessionapi.Manager, binding *Binding, connected wire.Snapshot, sessionID string) (*Binding, wire.Snapshot) {
	t.Helper()
	const replacementConfig = `[[Outline]]
Description = "replacement profile"
Server = "replacement.example.invalid"
Port = 443
Password = "replacement-synthetic-password"
`
	configured := binding.Configure(sessionID, int64(connected.Sequence), []byte(replacementConfig))
	var configuredResponse wire.Response[wire.Configuration]
	if err := json.Unmarshal([]byte(configured), &configuredResponse); err != nil || !configuredResponse.OK {
		t.Fatalf("configure replacement inventory: response=%s error=%v", configured, err)
	}
	if configuredResponse.Result.Digest == connected.ActiveDigest {
		t.Fatal("replacement inventory unexpectedly kept the active digest")
	}
	// A new binding represents a native UI reopening against the same process owner.
	reopened := NewForDesktop(manager)
	loaded := decodeBindingSnapshot(t, reopened, sessionID)
	if loaded.Digest != configuredResponse.Result.Digest || loaded.ActiveDigest != connected.ActiveDigest ||
		loaded.ActiveMode != string(sessionapi.ProfileIndex) || loaded.ActiveIndex != 1 ||
		loaded.ActiveProfile == nil || loaded.ActiveProfile.Description != "active manual profile" ||
		!loaded.CanSwitch || loaded.PrimaryAction != "STOP" {
		t.Fatalf("reopened Snapshot lost active identity or controls after inventory load: %#v", loaded)
	}
	return reopened, loaded
}

func startReplacementAndCheckPending(t *testing.T, manager *sessionapi.Manager, runtime *gatedSwitchRuntime, reopened *Binding, sessionID string, connected, loaded wire.Snapshot) {
	t.Helper()
	pendingResult := reopened.StartSelection(sessionID, int64(loaded.Sequence), string(sessionapi.ProfileIndex), 0, loaded.Digest, true)
	var pendingStart wire.Response[wire.Generation]
	if err := json.Unmarshal([]byte(pendingResult), &pendingStart); err != nil || !pendingStart.OK {
		t.Fatalf("start replacement profile: response=%s error=%v", pendingResult, err)
	}
	select {
	case <-runtime.stopEntered:
	case <-time.After(time.Second):
		t.Fatal("replacement did not wait for the active lease cleanup")
	}
	pending := decodeBindingSnapshot(t, NewForDesktop(manager), sessionID)
	if pending.ActiveDigest != connected.ActiveDigest || pending.ActiveMode != string(sessionapi.ProfileIndex) || pending.ActiveIndex != 1 ||
		pending.PendingTarget == nil || pending.PendingTarget.Digest != loaded.Digest ||
		pending.PendingTarget.Mode != string(sessionapi.ProfileIndex) || pending.PendingTarget.Index != 0 ||
		pending.CanSwitch || pending.PrimaryAction != "STOP" {
		t.Fatalf("reopened pending Snapshot lost selection metadata or controls: %#v", pending)
	}
}

func shutdownReopenedBinding(t *testing.T, reopened *Binding, runtime *gatedSwitchRuntime, release func(), sessionID string, connected wire.Snapshot) {
	t.Helper()
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- reopened.StopAndWait(context.Background()) }()
	waitForReopenedShutdownCancellation(t, reopened, sessionID, shutdownDone)
	release()
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("shutdown owner failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown owner did not finish after cleanup")
	}
	idle := waitBindingSnapshot(t, reopened, sessionID, string(sessionapi.StateIdle))
	if idle.PendingTarget != nil || idle.Generation != connected.Generation || idle.PrimaryAction != "START" {
		t.Fatalf("shutdown restarted a canceled replacement: %#v", idle)
	}
	select {
	case profile := <-runtime.starts:
		t.Fatalf("replacement profile started after shutdown cancellation: %#v", profile.Summary)
	default:
	}
}

func waitForReopenedShutdownCancellation(t *testing.T, reopened *Binding, sessionID string, shutdownDone <-chan error) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		current := decodeBindingSnapshot(t, reopened, sessionID)
		if current.PendingTarget == nil {
			if current.State != string(sessionapi.StateStopping) || current.PrimaryAction != "NONE" {
				t.Fatalf("shutdown owner exposed an invalid stopping snapshot: %#v", current)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("shutdown owner did not clear pending target: %#v", current)
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown returned before active lease cleanup: %v", err)
	default:
	}
}

func TestBindingPreservesStaleStopAndIdempotentStop(t *testing.T) {
	runtime := &blockingRuntime{}
	binding := NewForDesktop(sessionapi.NewManager(sessionapi.ManagerOptions{Runtime: runtime}))
	initial := binding.Snapshot("")
	sessionID := jsonSessionID(t, initial)
	configured := binding.Configure(sessionID, int64Field(t, initial, "sequence"), []byte(syntheticConfig))
	if !strings.Contains(configured, `"ok":true`) {
		t.Fatalf("configure failed: %s", configured)
	}
	started := binding.Start(sessionID, int64Field(t, configured, "sequence"), string(sessionapi.ProfileIndex), 0)
	generation := int64Field(t, started, "generation")
	stale := binding.Stop(sessionID, generation+1)
	if !strings.Contains(stale, `"STALE_GENERATION"`) {
		t.Fatalf("wrong stale result: %s", stale)
	}
	first := binding.Stop(sessionID, generation)
	second := binding.Stop(sessionID, generation)
	if !strings.Contains(first, `"ok":true`) || !strings.Contains(second, `"ok":true`) {
		t.Fatalf("stop command was not idempotent: first=%s second=%s", first, second)
	}
}

func TestCallbacksCarryTheSessionAndGeneration(t *testing.T) {
	platform := &recordingPlatform{}
	runtime := &blockingRuntime{}
	manager := sessionapi.NewManager(sessionapi.ManagerOptions{Runtime: runtime, Platform: platform})
	binding := NewForDesktop(manager)
	initial := binding.Snapshot("")
	sessionID := jsonSessionID(t, initial)
	configured := binding.Configure(sessionID, int64Field(t, initial, "sequence"), []byte(syntheticConfig))
	started := binding.Start(sessionID, int64Field(t, configured, "sequence"), string(sessionapi.ProfileIndex), 0)
	generation := int64Field(t, started, "generation")
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		platform.mu.Lock()
		seen := append([]sessionapi.StateChange(nil), platform.events...)
		platform.mu.Unlock()
		for _, event := range seen {
			if event.Generation == uint64(generation) {
				if event.SessionID != sessionID {
					t.Fatalf("callback lost session correlation: %#v", event)
				}
				_ = binding.Stop(sessionID, generation)
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("did not receive a generation-correlated state callback")
}

func TestTunnelOwnershipRejectsReuseUntilRelease(t *testing.T) {
	owners := newTunnelFDs()
	fd := int32(42)
	if !owners.reserve(fd, fdOwner{session: "session", generation: 1}) {
		t.Fatal("descriptor was not assigned to its generation")
	}
	if owners.reserve(fd, fdOwner{session: "session", generation: 2}) {
		t.Fatal("second generation reused an active descriptor")
	}
	owners.release(fd, fdOwner{session: "session", generation: 1})
	if !owners.reserve(fd, fdOwner{session: "session", generation: 2}) {
		t.Fatal("released descriptor ownership was not cleared")
	}
}

type blockingRuntime struct{}

func (r *blockingRuntime) Start(ctx context.Context, _ sessionapi.SessionRef, _ sessionapi.RuntimeProfile) (sessionapi.RuntimeLease, error) {
	return blockingLease{ctx: ctx}, nil
}

type blockingLease struct{ ctx context.Context }

func (l blockingLease) Stop(context.Context) error { return nil }

type recordingPlatform struct {
	mu     sync.Mutex
	events []sessionapi.StateChange
}

func (*recordingPlatform) PrepareTunnel(context.Context, sessionapi.SessionRef) (sessionapi.PlatformLease, error) {
	return noopLease{}, nil
}
func (*recordingPlatform) ProtectSocket(context.Context, sessionapi.SessionRef, int) error {
	return nil
}
func (p *recordingPlatform) PublishState(_ context.Context, event sessionapi.StateChange) {
	p.mu.Lock()
	p.events = append(p.events, event)
	p.mu.Unlock()
}

type noopLease struct{}

func (noopLease) Release(context.Context) error { return nil }

func jsonSessionID(t *testing.T, input string) string {
	t.Helper()
	const name = "session_id"
	needle := `"session_id":"`
	start := strings.Index(input, needle)
	if start < 0 {
		t.Fatalf("%q absent from %s", name, input)
	}
	value := input[start+len(needle):]
	end := strings.Index(value, `"`)
	if end < 0 {
		t.Fatalf("unterminated %q in %s", name, input)
	}
	return value[:end]
}
func int64Field(t *testing.T, input, name string) int64 {
	t.Helper()
	needle := `"` + name + `":`
	start := strings.Index(input, needle)
	if start < 0 {
		t.Fatalf("%q absent from %s", name, input)
	}
	var value int64
	for _, char := range input[start+len(needle):] {
		if char < '0' || char > '9' {
			break
		}
		value = value*10 + int64(char-'0')
	}
	return value
}
