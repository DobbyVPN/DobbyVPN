package desktoptransport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"go_module/grpcproto"
	v1 "go_module/sessionapi/v2"
)

func TestMappingsCoverPublicDomainValues(t *testing.T) {
	for input, want := range map[v1.Protocol]grpcproto.SessionProtocol{
		v1.ProtocolOutline:     grpcproto.SessionProtocol_SESSION_PROTOCOL_OUTLINE,
		v1.ProtocolXray:        grpcproto.SessionProtocol_SESSION_PROTOCOL_XRAY,
		v1.ProtocolTrustTunnel: grpcproto.SessionProtocol_SESSION_PROTOCOL_TRUST_TUNNEL,
	} {
		if got := Protocol(input); got != want {
			t.Fatalf("protocol %q = %v", input, got)
		}
	}
	for input, want := range map[v1.State]grpcproto.SessionState{
		v1.StateIdle:       grpcproto.SessionState_SESSION_STATE_IDLE,
		v1.StateConfigured: grpcproto.SessionState_SESSION_STATE_CONFIGURED,
		v1.StateProbing:    grpcproto.SessionState_SESSION_STATE_PROBING,
		v1.StatePreparing:  grpcproto.SessionState_SESSION_STATE_PREPARING,
		v1.StateConnected:  grpcproto.SessionState_SESSION_STATE_CONNECTED,
		v1.StateStopping:   grpcproto.SessionState_SESSION_STATE_STOPPING,
		v1.StateFailed:     grpcproto.SessionState_SESSION_STATE_FAILED,
	} {
		if got := State(input); got != want {
			t.Fatalf("state %q = %v", input, got)
		}
	}
	for input, want := range map[v1.FailureCode]grpcproto.SessionFailureCode{
		v1.FailureInvalidArgument: grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_INVALID_ARGUMENT,
		v1.FailureNotFound:        grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_NOT_FOUND,
		v1.FailureConflict:        grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_CONFLICT,
		v1.FailureNotConfigured:   grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_NOT_CONFIGURED,
		v1.FailureStaleGeneration: grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_STALE_GENERATION,
		v1.FailureUnsupported:     grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_UNSUPPORTED,
		v1.FailureMalformedConfig: grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_MALFORMED_CONFIG,
		v1.FailureProbe:           grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_PROBE_FAILED,
		v1.FailurePlatform:        grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_PLATFORM_FAILED,
		v1.FailureRuntime:         grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_RUNTIME_FAILED,
		v1.FailureCanceled:        grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_CANCELED,
		v1.FailureInternal:        grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_INTERNAL,
		v1.FailureCleanup:         grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_CLEANUP_FAILED,
	} {
		if got := FailureCode(input); got != want {
			t.Fatalf("failure %q = %v", input, got)
		}
	}
	if got := Failure(context.Canceled); got.GetCode() != grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_INTERNAL || got.GetMessage() != "internal session error" {
		t.Fatalf("untyped error was not sanitized = %#v", got)
	}
}

func TestTypedFailureOmitsInternalCause(t *testing.T) {
	err := &v1.Error{Code: v1.FailureInvalidArgument, Message: "configuration URL could not be fetched", Cause: context.DeadlineExceeded}
	got := Failure(err)
	if got.GetMessage() != "configuration URL could not be fetched" || got.GetCode() != grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_INVALID_ARGUMENT {
		t.Fatalf("typed failure exposed its cause or lost its public message = %#v", got)
	}
}

func TestSnapshotMappingCarriesAuthoritativeConfiguration(t *testing.T) {
	in := v1.SnapshotResult{
		SessionID: "session", Sequence: 8, Generation: 3, State: v1.StateConnected,
		Configured: true, Digest: "digest", SourceKind: v1.ConfigSourceURL,
		Profiles:      []v1.ProfileSummary{{Index: 0, Protocol: v1.ProtocolOutline, Description: "primary"}},
		Warnings:      []v1.Warning{{Code: "OPTIONAL", Message: "optional setting ignored"}},
		ActiveProfile: &v1.ProfileSummary{Index: 0, Protocol: v1.ProtocolOutline, Description: "primary"},
		LastFailure:   v1.FailureRuntime, LastFailureMessage: "runtime stopped",
	}
	out := Snapshot(in)
	if out.GetSessionId() != in.SessionID || out.GetSequence() != in.Sequence || out.GetGeneration() != in.Generation || out.GetState() != grpcproto.SessionState_SESSION_STATE_CONNECTED {
		t.Fatalf("identity and state fields were not preserved: %#v", out)
	}
	if !out.GetConfigured() || out.GetDigest() != in.Digest || out.GetSourceKind() != grpcproto.SessionSourceKind_SESSION_SOURCE_KIND_URL {
		t.Fatalf("configuration identity was not preserved: %#v", out)
	}
	if len(out.GetProfiles()) != 1 || out.GetProfiles()[0].GetProtocol() != grpcproto.SessionProtocol_SESSION_PROTOCOL_OUTLINE || len(out.GetWarnings()) != 1 {
		t.Fatalf("configuration metadata was not preserved: %#v", out)
	}
	if out.GetActiveProfile().GetDescription() != "primary" || out.GetLastFailure().GetCode() != grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_RUNTIME_FAILED || out.GetLastFailure().GetMessage() != "runtime stopped" {
		t.Fatalf("runtime state was not preserved: %#v", out)
	}
}

func TestManagerValidationAndConfigurationReceiveExactRawBytes(t *testing.T) {
	m := v1.NewManager(v1.ManagerOptions{Runtime: mapperRuntime{}, Platform: mapperPlatform{}})
	initial, err := m.Snapshot(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("\n[[Outline]]\nServer = \"vpn.invalid\"\nPort = 443\nPassword = \"secret\"\n")
	validated, err := m.ValidateConfig(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	if validated.Digest != hex.EncodeToString(digest[:]) {
		t.Fatalf("raw bytes changed before validation: %q", validated.Digest)
	}
	afterValidation, err := m.Snapshot(context.Background(), initial.SessionID)
	if err != nil || afterValidation.Sequence != initial.Sequence || afterValidation.Configured {
		t.Fatalf("ValidateConfig mutated state: %#v, %v", afterValidation, err)
	}
	configured, err := m.Configure(context.Background(), initial.SessionID, initial.Sequence, raw)
	if err != nil {
		t.Fatal(err)
	}
	if configured.Digest != hex.EncodeToString(digest[:]) || configured.Sequence <= initial.Sequence {
		t.Fatalf("Configure changed bytes or failed to advance revision: %#v", configured)
	}
	snapshot, err := m.Snapshot(context.Background(), initial.SessionID)
	if err != nil || snapshot.Digest != configured.Digest || snapshot.SourceKind != v1.ConfigSourceInline || len(snapshot.Profiles) != len(configured.Profiles) {
		t.Fatalf("snapshot did not carry accepted configuration: %#v, %v", snapshot, err)
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
func (mapperPlatform) PublishState(context.Context, v1.StateChange)            {}

type mapperPlatformLease struct{}

func (mapperPlatformLease) Release(context.Context) error { return nil }
