package desktoptransport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"go_module/grpcproto"
	v1 "go_module/sessionapi/v2"
)

func TestMappingsCoverPublicDomainValues(t *testing.T) {
	for input, want := range map[v1.Protocol]grpcproto.SessionProtocol{
		v1.ProtocolOutline: grpcproto.SessionProtocol_SESSION_PROTOCOL_OUTLINE, v1.ProtocolXray: grpcproto.SessionProtocol_SESSION_PROTOCOL_XRAY, v1.ProtocolTrustTunnel: grpcproto.SessionProtocol_SESSION_PROTOCOL_TRUST_TUNNEL,
	} {
		if got := Protocol(input); got != want {
			t.Fatalf("protocol %q = %v", input, got)
		}
	}
	for input, want := range map[v1.FailureCode]grpcproto.SessionFailureCode{
		v1.FailureInvalidArgument: grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_INVALID_ARGUMENT, v1.FailureNotFound: grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_NOT_FOUND, v1.FailureConflict: grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_CONFLICT, v1.FailureNotConfigured: grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_NOT_CONFIGURED, v1.FailureStaleGeneration: grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_STALE_GENERATION, v1.FailureUnsupported: grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_UNSUPPORTED, v1.FailureMalformedConfig: grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_MALFORMED_CONFIG, v1.FailureProbe: grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_PROBE_FAILED, v1.FailurePlatform: grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_PLATFORM_FAILED, v1.FailureRuntime: grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_RUNTIME_FAILED, v1.FailureCanceled: grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_CANCELED, v1.FailureInternal: grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_INTERNAL, v1.FailureCleanup: grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_CLEANUP_FAILED,
	} {
		if got := FailureCode(input); got != want {
			t.Fatalf("failure %q = %v", input, got)
		}
	}
	if got := Failure(context.Canceled); got.GetCode() != grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_INTERNAL || got.GetMessage() != "operation failed" {
		t.Fatalf("unsafe error = %#v", got)
	}
}

func TestManagerConfigureReceivesExactRawBytesAndKeepsOrderedEvents(t *testing.T) {
	m := v1.NewManager(v1.ManagerOptions{Runtime: mapperRuntime{}, Platform: mapperPlatform{}})
	id, err := m.CreateSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("\n[[Outline]]\nServer = \"vpn.invalid\"\nPort = 443\nPassword = \"secret\"\n")
	configured, err := m.Configure(context.Background(), id, "configure", raw)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	if configured.Digest != hex.EncodeToString(digest[:]) {
		t.Fatalf("raw bytes changed before Configure: %q", configured.Digest)
	}
	started, err := m.Start(context.Background(), id, "start", v1.StartTarget{Mode: v1.ProfileIndex, Index: 0})
	if err != nil {
		t.Fatal(err)
	}
	if duplicate, duplicateErr := m.Start(context.Background(), id, "start", v1.StartTarget{Mode: v1.ProfileIndex, Index: 0}); duplicateErr != nil || duplicate.Generation != started.Generation {
		t.Fatalf("duplicate start = %#v, %v", duplicate, duplicateErr)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshot, _ := m.Snapshot(context.Background(), id)
		if snapshot.State == v1.StateConnected {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if _, stopErr := m.Stop(context.Background(), id, "stale", started.Generation+1); v1.CodeOf(stopErr) != v1.FailureStaleGeneration {
		t.Fatalf("stale generation = %v", stopErr)
	}
	observed, err := m.Observe(context.Background(), id, 0)
	if err != nil {
		t.Fatal(err)
	}
	for index, event := range observed.Events {
		if event.Sequence != uint64(index+1) {
			t.Fatalf("events not ordered at %d: %#v", index, event)
		}
	}
}

type mapperRuntime struct{}

func (mapperRuntime) Probe(context.Context, v1.SessionRef, v1.RuntimeProfile) (v1.ProbeResult, error) {
	return v1.ProbeResult{LatencyMillis: 1}, nil
}
func (mapperRuntime) Start(context.Context, v1.SessionRef, v1.RuntimeProfile) (v1.RuntimeLease, error) {
	return mapperLease{}, nil
}

type mapperLease struct{}

func (mapperLease) Stop(context.Context) error { return nil }

type mapperPlatform struct{}

func (mapperPlatform) PrepareTunnel(context.Context, v1.SessionRef) (v1.PlatformLease, error) {
	return mapperPlatformLease{}, nil
}
func (mapperPlatform) ProtectSocket(context.Context, v1.SessionRef, int) error { return nil }
func (mapperPlatform) PublishState(context.Context, v1.Event) error            { return nil }

type mapperPlatformLease struct{}

func (mapperPlatformLease) Release(context.Context) error { return nil }
