package grpctransport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"go_module/grpcproto"
	"go_module/sessionapi"
)

func TestMappingsCoverPublicDomainValues(t *testing.T) {
	for input, want := range map[sessionapi.Protocol]grpcproto.SessionProtocol{
		sessionapi.ProtocolOutline:     grpcproto.SessionProtocol_SESSION_PROTOCOL_OUTLINE,
		sessionapi.ProtocolXray:        grpcproto.SessionProtocol_SESSION_PROTOCOL_XRAY,
		sessionapi.ProtocolTrustTunnel: grpcproto.SessionProtocol_SESSION_PROTOCOL_TRUST_TUNNEL,
	} {
		if got := protocol(input); got != want {
			t.Fatalf("protocol %q = %v", input, got)
		}
	}
	for input, want := range map[sessionapi.State]grpcproto.SessionState{
		sessionapi.StateIdle:       grpcproto.SessionState_SESSION_STATE_IDLE,
		sessionapi.StateConfigured: grpcproto.SessionState_SESSION_STATE_CONFIGURED,
		sessionapi.StateProbing:    grpcproto.SessionState_SESSION_STATE_PROBING,
		sessionapi.StatePreparing:  grpcproto.SessionState_SESSION_STATE_PREPARING,
		sessionapi.StateConnected:  grpcproto.SessionState_SESSION_STATE_CONNECTED,
		sessionapi.StateStopping:   grpcproto.SessionState_SESSION_STATE_STOPPING,
		sessionapi.StateFailed:     grpcproto.SessionState_SESSION_STATE_FAILED,
	} {
		if got := state(input); got != want {
			t.Fatalf("state %q = %v", input, got)
		}
	}
	for input, want := range map[sessionapi.FailureCode]grpcproto.SessionFailureCode{
		sessionapi.FailureInvalidArgument: grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_INVALID_ARGUMENT,
		sessionapi.FailureNotFound:        grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_NOT_FOUND,
		sessionapi.FailureConflict:        grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_CONFLICT,
		sessionapi.FailureNotConfigured:   grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_NOT_CONFIGURED,
		sessionapi.FailureStaleGeneration: grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_STALE_GENERATION,
		sessionapi.FailureUnsupported:     grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_UNSUPPORTED,
		sessionapi.FailureMalformedConfig: grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_MALFORMED_CONFIG,
		sessionapi.FailureProbe:           grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_PROBE_FAILED,
		sessionapi.FailurePlatform:        grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_PLATFORM_FAILED,
		sessionapi.FailureRuntime:         grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_RUNTIME_FAILED,
		sessionapi.FailureCanceled:        grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_CANCELED,
		sessionapi.FailureInternal:        grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_INTERNAL,
		sessionapi.FailureCleanup:         grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_CLEANUP_FAILED,
	} {
		if got := failureCode(input); got != want {
			t.Fatalf("failure %q = %v", input, got)
		}
	}
	if got := failure(context.Canceled); got.GetCode() != grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_INTERNAL || got.GetMessage() != "internal session error" {
		t.Fatalf("untyped error was not sanitized = %#v", got)
	}
}

func TestTypedFailureOmitsInternalCause(t *testing.T) {
	err := &sessionapi.Error{Code: sessionapi.FailureInvalidArgument, Message: "configuration URL could not be fetched", Cause: context.DeadlineExceeded}
	got := failure(err)
	if got.GetMessage() != "configuration URL could not be fetched" || got.GetCode() != grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_INVALID_ARGUMENT {
		t.Fatalf("typed failure exposed its cause or lost its public message = %#v", got)
	}
}

func TestSnapshotMappingCarriesAuthoritativeConfiguration(t *testing.T) {
	in := sessionapi.SnapshotResult{
		SessionID: "session", Sequence: 8, Generation: 3, State: sessionapi.StateConnected,
		Configured: true, Digest: "digest", SourceKind: sessionapi.ConfigSourceURL,
		Profiles:      []sessionapi.ProfileSummary{{Index: 0, Protocol: sessionapi.ProtocolOutline, Description: "primary"}},
		Warnings:      []sessionapi.Warning{{Code: "OPTIONAL", Message: "optional setting ignored"}},
		ActiveProfile: &sessionapi.ProfileSummary{Index: 0, Protocol: sessionapi.ProtocolOutline, Description: "primary"},
		LastFailure:   sessionapi.FailureRuntime, LastFailureMessage: "runtime stopped", Recovering: true,
	}
	out := snapshot(in)
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
	if !out.GetRecovering() {
		t.Fatalf("recovery state was not preserved: %#v", out)
	}
}

func TestManagerValidationAndConfigurationReceiveExactRawBytes(t *testing.T) {
	m := sessionapi.NewManager(sessionapi.ManagerOptions{Runtime: mapperRuntime{}, Platform: mapperPlatform{}})
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
	if err != nil || snapshot.Digest != configured.Digest || snapshot.SourceKind != sessionapi.ConfigSourceInline || len(snapshot.Profiles) != len(configured.Profiles) {
		t.Fatalf("snapshot did not carry accepted configuration: %#v, %v", snapshot, err)
	}
}

type mapperRuntime struct{}

func (mapperRuntime) Probe(context.Context, sessionapi.SessionRef, sessionapi.RuntimeProfile) (sessionapi.ProbeResult, error) {
	return sessionapi.ProbeResult{LatencyMillis: 1}, nil
}
func (mapperRuntime) Start(context.Context, sessionapi.SessionRef, sessionapi.RuntimeProfile) (sessionapi.RuntimeLease, error) {
	return mapperLease{}, nil
}

type mapperLease struct{}

func (mapperLease) Stop(context.Context) error { return nil }

type mapperPlatform struct{}

func (mapperPlatform) PrepareTunnel(context.Context, sessionapi.SessionRef) (sessionapi.PlatformLease, error) {
	return mapperPlatformLease{}, nil
}
func (mapperPlatform) ProtectSocket(context.Context, sessionapi.SessionRef, int) error { return nil }
func (mapperPlatform) PublishState(context.Context, sessionapi.StateChange)            {}

type mapperPlatformLease struct{}

func (mapperPlatformLease) Release(context.Context) error { return nil }
