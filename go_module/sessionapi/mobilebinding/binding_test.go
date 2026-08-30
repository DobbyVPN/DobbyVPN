package mobilebinding

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "go_module/sessionapi/v2"
)

const sensitiveConfig = `[[Outline]]
Server = "vpn.example.invalid"
Port = 443
Password = "super-secret-token"
`

func TestJSONEnvelopeRedactsConfigurationAndUsesStableKeys(t *testing.T) {
	binding := NewForTest(v1.NewManager(v1.ManagerOptions{}))
	created := binding.CreateSession()
	if strings.Contains(created, "SessionID") || !strings.Contains(created, `"session_id"`) {
		t.Fatalf("create response did not use stable snake_case: %s", created)
	}
	sessionID := jsonField(t, created, "session_id")
	result := binding.Configure(sessionID, "configure-1", []byte(sensitiveConfig))
	for _, secret := range []string{"super-secret-token", "vpn.example.invalid", "Password"} {
		if strings.Contains(result, secret) {
			t.Fatalf("safe result leaked %q: %s", secret, result)
		}
	}
	if !strings.Contains(result, `"profiles"`) || strings.Contains(result, `"Profiles"`) {
		t.Fatalf("configure response did not use stable DTO keys: %s", result)
	}
}

func TestBindingPreservesStaleStopAndIdempotentStop(t *testing.T) {
	runtime := &blockingRuntime{}
	binding := NewForTest(v1.NewManager(v1.ManagerOptions{Runtime: runtime}))
	sessionID := jsonField(t, binding.CreateSession(), "session_id")
	if result := binding.Configure(sessionID, "configure", []byte(sensitiveConfig)); !strings.Contains(result, `"ok":true`) {
		t.Fatalf("configure failed: %s", result)
	}
	started := binding.Start(sessionID, "start", string(v1.ProfileIndex), 0)
	generation := int64Field(t, started, "generation")
	stale := binding.Stop(sessionID, "stop-stale", generation+1)
	if !strings.Contains(stale, `"STALE_GENERATION"`) {
		t.Fatalf("wrong stale result: %s", stale)
	}
	first := binding.Stop(sessionID, "stop", generation)
	second := binding.Stop(sessionID, "stop", generation)
	if first != second || !strings.Contains(first, `"ok":true`) {
		t.Fatalf("stop command was not idempotent: first=%s second=%s", first, second)
	}
}

func TestCallbacksCarryTheSessionAndGeneration(t *testing.T) {
	platform := &recordingPlatform{}
	runtime := &blockingRuntime{}
	manager := v1.NewManager(v1.ManagerOptions{Runtime: runtime, Platform: platform})
	binding := NewForTest(manager)
	sessionID := jsonField(t, binding.CreateSession(), "session_id")
	binding.Configure(sessionID, "configure", []byte(sensitiveConfig))
	started := binding.Start(sessionID, "start", string(v1.ProfileIndex), 0)
	generation := int64Field(t, started, "generation")
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		platform.mu.Lock()
		seen := append([]v1.Event(nil), platform.events...)
		platform.mu.Unlock()
		for _, event := range seen {
			if event.Generation == uint64(generation) {
				if event.SessionID != sessionID {
					t.Fatalf("callback lost session correlation: %#v", event)
				}
				_ = binding.Stop(sessionID, "stop", generation)
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
	events []v1.Event
}

func (*recordingPlatform) PrepareTunnel(context.Context, v1.SessionRef) (v1.PlatformLease, error) {
	return noopLease{}, nil
}
func (*recordingPlatform) ProtectSocket(context.Context, v1.SessionRef, int) error { return nil }
func (p *recordingPlatform) PublishState(_ context.Context, event v1.Event) error {
	p.mu.Lock()
	p.events = append(p.events, event)
	p.mu.Unlock()
	return nil
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
