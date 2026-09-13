package mobilebinding

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "go_module/sessionapi/v2"
)

const syntheticConfig = `[[Outline]]
Server = "vpn.example.invalid"
Port = 443
Password = "super-secret-token"
`

func TestJSONEnvelopeUsesStableKeys(t *testing.T) {
	binding := NewForTest(v1.NewManager(v1.ManagerOptions{}))
	initial := binding.Snapshot("")
	if strings.Contains(initial, "SessionID") || !strings.Contains(initial, `"session_id"`) {
		t.Fatalf("snapshot did not use stable snake_case: %s", initial)
	}
	sessionID := jsonField(t, initial, "session_id")
	configured := binding.Configure(sessionID, int64Field(t, initial, "sequence"), []byte(syntheticConfig))
	for _, field := range []string{`"sequence"`, `"source_kind":"INLINE"`, `"profiles"`, `"warnings"`} {
		if !strings.Contains(configured, field) || strings.Contains(configured, `"Profiles"`) {
			t.Fatalf("configure response did not use stable complete DTO keys: %s", configured)
		}
	}
}

func TestInternalFailureEnvelopeDoesNotExposeRawErrors(t *testing.T) {
	result := failed(errors.New("exact mobile failure"))
	if !strings.Contains(result, `"code":"INTERNAL"`) || !strings.Contains(result, `"message":"internal session error"`) || strings.Contains(result, "exact mobile failure") {
		t.Fatalf("failure envelope exposed an untyped internal error: %s", result)
	}
}

func TestURLFetchFailureDoesNotEchoSourceCredentials(t *testing.T) {
	url := "https://alice:secret@example.invalid/profile?token=private"
	binding := NewForTest(v1.NewManager(v1.ManagerOptions{Loader: failingURLLoader{}}))
	initial := binding.Snapshot("")
	result := binding.Configure(jsonField(t, initial, "session_id"), int64Field(t, initial, "sequence"), []byte(url))
	for _, sensitive := range []string{"alice", "secret", "example.invalid", "private", url} {
		if strings.Contains(result, sensitive) {
			t.Fatalf("validation response leaked %q: %s", sensitive, result)
		}
	}
	if !strings.Contains(result, "configuration URL could not be fetched") {
		t.Fatalf("validation response lost the safe failure reason: %s", result)
	}
}

func TestSnapshotCarriesAcceptedConfigurationAndResetClearsIt(t *testing.T) {
	binding := NewForTest(v1.NewManager(v1.ManagerOptions{}))
	initial := binding.Snapshot("")
	sessionID := jsonField(t, initial, "session_id")
	configured := binding.Configure(sessionID, int64Field(t, initial, "sequence"), []byte(syntheticConfig))
	if strings.Contains(configured, "super-secret-token") {
		t.Fatalf("configuration bytes leaked in Configure result: %s", configured)
	}
	snapshot := binding.Snapshot(sessionID)
	for _, field := range []string{`"sequence":`, `"digest":`, `"source_kind":"INLINE"`, `"profiles":[`, `"warnings":[`} {
		if !strings.Contains(snapshot, field) {
			t.Fatalf("snapshot missing %s: %s", field, snapshot)
		}
	}
	if strings.Contains(snapshot, "super-secret-token") {
		t.Fatalf("configuration bytes leaked in Snapshot: %s", snapshot)
	}
	reset := binding.Reset(sessionID, int64Field(t, snapshot, "sequence"))
	if !strings.Contains(reset, `"state":"IDLE"`) || !strings.Contains(reset, `"configured":false`) || jsonField(t, reset, "session_id") != sessionID {
		t.Fatalf("Reset did not retain identity and clear accepted state: %s", reset)
	}
}

func TestBindingPreservesStaleStopAndIdempotentStop(t *testing.T) {
	runtime := &blockingRuntime{}
	binding := NewForTest(v1.NewManager(v1.ManagerOptions{Runtime: runtime}))
	initial := binding.Snapshot("")
	sessionID := jsonField(t, initial, "session_id")
	configured := binding.Configure(sessionID, int64Field(t, initial, "sequence"), []byte(syntheticConfig))
	if !strings.Contains(configured, `"ok":true`) {
		t.Fatalf("configure failed: %s", configured)
	}
	started := binding.Start(sessionID, int64Field(t, configured, "sequence"), string(v1.ProfileIndex), 0)
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
	manager := v1.NewManager(v1.ManagerOptions{Runtime: runtime, Platform: platform})
	binding := NewForTest(manager)
	initial := binding.Snapshot("")
	sessionID := jsonField(t, initial, "session_id")
	configured := binding.Configure(sessionID, int64Field(t, initial, "sequence"), []byte(syntheticConfig))
	started := binding.Start(sessionID, int64Field(t, configured, "sequence"), string(v1.ProfileIndex), 0)
	generation := int64Field(t, started, "generation")
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		platform.mu.Lock()
		seen := append([]v1.StateChange(nil), platform.events...)
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

type failingURLLoader struct{}

func (failingURLLoader) Load(_ context.Context, raw []byte) (v1.LoadedConfig, error) {
	return v1.LoadedConfig{}, errors.New("request failed for " + string(raw))
}

func (r *blockingRuntime) Probe(context.Context, v1.SessionRef, v1.RuntimeProfile) (v1.ProbeResult, error) {
	return v1.ProbeResult{}, nil
}
func (r *blockingRuntime) Start(ctx context.Context, _ v1.SessionRef, _ v1.RuntimeProfile) (v1.RuntimeLease, error) {
	return blockingLease{ctx: ctx}, nil
}

type blockingLease struct{ ctx context.Context }

func (l blockingLease) Stop(context.Context) error { return nil }

type recordingPlatform struct {
	mu     sync.Mutex
	events []v1.StateChange
}

func (*recordingPlatform) PrepareTunnel(context.Context, v1.SessionRef) (v1.PlatformLease, error) {
	return noopLease{}, nil
}
func (*recordingPlatform) ProtectSocket(context.Context, v1.SessionRef, int) error { return nil }
func (p *recordingPlatform) PublishState(_ context.Context, event v1.StateChange) {
	p.mu.Lock()
	p.events = append(p.events, event)
	p.mu.Unlock()
}

type noopLease struct{}

func (noopLease) Release(context.Context) error { return nil }

func jsonField(t *testing.T, input, name string) string {
	t.Helper()
	needle := `"` + name + `":"`
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
