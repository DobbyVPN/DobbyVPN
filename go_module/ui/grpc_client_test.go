package ui

import (
	"testing"

	"go_module/grpcproto"
)

func TestSnapshotConversionUsesStableUIEnumNames(t *testing.T) {
	value := snapshot(&grpcproto.SessionSnapshot{
		SessionId:  "session",
		Sequence:   8,
		Generation: 2,
		State:      grpcproto.SessionState_SESSION_STATE_CONNECTED,
		Configured: true,
		SourceKind: grpcproto.SessionSourceKind_SESSION_SOURCE_KIND_URL,
		Profiles: []*grpcproto.SessionProfile{{
			Index:       0,
			Protocol:    grpcproto.SessionProtocol_SESSION_PROTOCOL_OUTLINE,
			Description: "primary",
		}},
		ActiveProfile: &grpcproto.SessionProfile{
			Index:       0,
			Protocol:    grpcproto.SessionProtocol_SESSION_PROTOCOL_OUTLINE,
			Description: "primary",
		},
		LastFailure: &grpcproto.SessionFailure{
			Code:    grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_RUNTIME_FAILED,
			Message: "runtime stopped",
		},
	})

	if value.State != StateConnected || value.SourceKind != "URL" {
		t.Fatalf("unexpected state/source: %#v", value)
	}
	if value.ActiveProfile == nil || value.ActiveProfile.Protocol != ProtocolOutline {
		t.Fatalf("unexpected active profile: %#v", value.ActiveProfile)
	}
	if value.LastFailure == nil || value.LastFailure.Code != "RUNTIME_FAILED" {
		t.Fatalf("unexpected failure: %#v", value.LastFailure)
	}
}

func TestEnumConversionDoesNotExposeGeneratedPrefixes(t *testing.T) {
	if got := stateName(grpcproto.SessionState_SESSION_STATE_IDLE); got != "IDLE" {
		t.Fatalf("state = %q", got)
	}
	if got := protocolName(grpcproto.SessionProtocol_SESSION_PROTOCOL_TRUST_TUNNEL); got != "TRUST_TUNNEL" {
		t.Fatalf("protocol = %q", got)
	}
	if got := failureName(grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_INVALID_ARGUMENT); got != "INVALID_ARGUMENT" {
		t.Fatalf("failure = %q", got)
	}
}
