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
)

const syntheticConfig = `schema_version = 2
[[profiles]]
protocol = "OUTLINE"
[profiles.config]
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
		Error envelopeError `json:"error"`
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
	binding := NewForTest(sessionapi.NewManager(sessionapi.ManagerOptions{}))
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
	binding := NewForTest(sessionapi.NewManager(sessionapi.ManagerOptions{Loader: acceptedURLLoader{}}))
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
	binding := NewForTest(sessionapi.NewManager(sessionapi.ManagerOptions{}))
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
	binding := NewForTest(sessionapi.NewManager(sessionapi.ManagerOptions{}))
	initial := binding.Snapshot("")
	params := json.RawMessage(`{"session_id":"` + jsonSessionID(t, initial) + `","expected_sequence":` + strconv.FormatInt(int64Field(t, initial, "sequence"), 10) + `}`)
	response := binding.CallJSONWithConfiguration(
		context.Background(), "Configure", params, []byte{}, true,
	)
	if !strings.Contains(response, `"code":"MALFORMED_CONFIG"`) {
		t.Fatalf("empty config was not rejected by the shared Go configuration path: %s", response)
	}
}

func TestStartJSONAcceptsSourceInRequestOrSeparateMailbox(t *testing.T) {
	for _, separate := range []bool{false, true} {
		manager := sessionapi.NewManager(sessionapi.ManagerOptions{
			Loader: acceptedURLLoader{}, Runtime: &blockingRuntime{}, Platform: &recordingPlatform{},
		})
		binding := NewForTest(manager)
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
	binding := NewForTest(sessionapi.NewManager(sessionapi.ManagerOptions{}))
	initial := binding.Snapshot("")
	params := json.RawMessage(`{"session_id":"` + jsonSessionID(t, initial) + `","expected_sequence":` +
		strconv.FormatInt(int64Field(t, initial, "sequence"), 10) + `,"mode":"AUTO_SELECT","index":0}`)
	response := binding.CallJSONWithConfiguration(context.Background(), "Start", params, nil, true)
	if !strings.Contains(response, `"code":"MALFORMED_CONFIG"`) {
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
		encoded, err := json.Marshal(snapshotDTO(sessionapi.SnapshotResult{Recovering: recovering}))
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
	binding := NewForTest(sessionapi.NewManager(sessionapi.ManagerOptions{}))
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

func TestBindingPreservesStaleStopAndIdempotentStop(t *testing.T) {
	runtime := &blockingRuntime{}
	binding := NewForTest(sessionapi.NewManager(sessionapi.ManagerOptions{Runtime: runtime}))
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
	binding := NewForTest(manager)
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
