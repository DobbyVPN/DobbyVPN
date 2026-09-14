// Package desktoptransport contains native-free session response conversion.
// Desktop gRPC owns construction of the native runtime; this package is kept
// free of protocol/device imports so its safety and ordering tests run on a
// normal Go toolchain.
package desktoptransport

import (
	"errors"
	"fmt"

	"go_module/grpcproto"
	v2 "go_module/sessionapi/v2"
)

func Protocol(protocol v2.Protocol) grpcproto.SessionProtocol {
	switch protocol {
	case v2.ProtocolOutline:
		return grpcproto.SessionProtocol_SESSION_PROTOCOL_OUTLINE
	case v2.ProtocolXray:
		return grpcproto.SessionProtocol_SESSION_PROTOCOL_XRAY
	case v2.ProtocolTrustTunnel:
		return grpcproto.SessionProtocol_SESSION_PROTOCOL_TRUST_TUNNEL
	default:
		panic(fmt.Sprintf("unsupported session protocol %q", protocol))
	}
}

func State(state v2.State) grpcproto.SessionState {
	switch state {
	case v2.StateIdle:
		return grpcproto.SessionState_SESSION_STATE_IDLE
	case v2.StateConfigured:
		return grpcproto.SessionState_SESSION_STATE_CONFIGURED
	case v2.StateProbing:
		return grpcproto.SessionState_SESSION_STATE_PROBING
	case v2.StatePreparing:
		return grpcproto.SessionState_SESSION_STATE_PREPARING
	case v2.StateConnected:
		return grpcproto.SessionState_SESSION_STATE_CONNECTED
	case v2.StateStopping:
		return grpcproto.SessionState_SESSION_STATE_STOPPING
	case v2.StateFailed:
		return grpcproto.SessionState_SESSION_STATE_FAILED
	default:
		panic(fmt.Sprintf("unsupported session state %q", state))
	}
}

func FailureCode(code v2.FailureCode) grpcproto.SessionFailureCode {
	switch code {
	case v2.FailureInvalidArgument:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_INVALID_ARGUMENT
	case v2.FailureNotFound:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_NOT_FOUND
	case v2.FailureConflict:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_CONFLICT
	case v2.FailureNotConfigured:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_NOT_CONFIGURED
	case v2.FailureStaleGeneration:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_STALE_GENERATION
	case v2.FailureUnsupported:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_UNSUPPORTED
	case v2.FailureMalformedConfig:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_MALFORMED_CONFIG
	case v2.FailureProbe:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_PROBE_FAILED
	case v2.FailurePlatform:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_PLATFORM_FAILED
	case v2.FailureRuntime:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_RUNTIME_FAILED
	case v2.FailureCanceled:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_CANCELED
	case v2.FailureInternal:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_INTERNAL
	case v2.FailureCleanup:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_CLEANUP_FAILED
	default:
		panic(fmt.Sprintf("unsupported session failure code %q", code))
	}
}

func Failure(err error) *grpcproto.SessionFailure {
	if err == nil {
		return nil
	}
	var domain *v2.Error
	if errors.As(err, &domain) {
		return &grpcproto.SessionFailure{Code: FailureCode(domain.Code), Message: domain.Message}
	}
	return &grpcproto.SessionFailure{Code: grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_INTERNAL, Message: "internal session error"}
}

func SourceKind(kind v2.ConfigSourceKind) grpcproto.SessionSourceKind {
	switch kind {
	case v2.ConfigSourceInline:
		return grpcproto.SessionSourceKind_SESSION_SOURCE_KIND_INLINE
	case v2.ConfigSourceURL:
		return grpcproto.SessionSourceKind_SESSION_SOURCE_KIND_URL
	default:
		return grpcproto.SessionSourceKind_SESSION_SOURCE_KIND_UNSPECIFIED
	}
}

func Profile(in v2.ProfileSummary) *grpcproto.SessionProfile {
	return &grpcproto.SessionProfile{Index: in.Index, Protocol: Protocol(in.Protocol), Description: in.Description}
}

func Profiles(in []v2.ProfileSummary) []*grpcproto.SessionProfile {
	out := make([]*grpcproto.SessionProfile, 0, len(in))
	for _, profile := range in {
		out = append(out, Profile(profile))
	}
	return out
}

func Warnings(in []v2.Warning) []*grpcproto.SessionWarning {
	out := make([]*grpcproto.SessionWarning, 0, len(in))
	for _, warning := range in {
		out = append(out, &grpcproto.SessionWarning{Code: warning.Code, Message: warning.Message})
	}
	return out
}

func Snapshot(in v2.SnapshotResult) *grpcproto.SessionSnapshot {
	out := &grpcproto.SessionSnapshot{
		SessionId: in.SessionID, Sequence: in.Sequence, Generation: in.Generation,
		State: State(in.State), Configured: in.Configured, Digest: in.Digest,
		SourceKind: SourceKind(in.SourceKind), Profiles: Profiles(in.Profiles),
		Warnings: Warnings(in.Warnings), CleanupComplete: in.CleanupComplete,
		Recovering: in.Recovering,
	}
	if in.ActiveProfile != nil {
		out.ActiveProfile = Profile(*in.ActiveProfile)
	}
	if in.LastFailure != "" {
		out.LastFailure = &grpcproto.SessionFailure{Code: FailureCode(in.LastFailure), Message: in.LastFailureMessage}
	}
	return out
}
