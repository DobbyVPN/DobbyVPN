// Native-free response conversion stays beside the gRPC handler. The package
// accepts injected managers, so its safety and ordering tests do not construct
// a native runtime.
package grpctransport

import (
	"errors"
	"fmt"

	"go_module/grpcproto"
	"go_module/sessionapi"
)

func protocol(protocol sessionapi.Protocol) grpcproto.SessionProtocol {
	switch protocol {
	case sessionapi.ProtocolOutline:
		return grpcproto.SessionProtocol_SESSION_PROTOCOL_OUTLINE
	case sessionapi.ProtocolXray:
		return grpcproto.SessionProtocol_SESSION_PROTOCOL_XRAY
	case sessionapi.ProtocolTrustTunnel:
		return grpcproto.SessionProtocol_SESSION_PROTOCOL_TRUST_TUNNEL
	default:
		panic(fmt.Sprintf("unsupported session protocol %q", protocol))
	}
}

func state(state sessionapi.State) grpcproto.SessionState {
	switch state {
	case sessionapi.StateIdle:
		return grpcproto.SessionState_SESSION_STATE_IDLE
	case sessionapi.StateConfigured:
		return grpcproto.SessionState_SESSION_STATE_CONFIGURED
	case sessionapi.StateProbing:
		return grpcproto.SessionState_SESSION_STATE_PROBING
	case sessionapi.StatePreparing:
		return grpcproto.SessionState_SESSION_STATE_PREPARING
	case sessionapi.StateConnected:
		return grpcproto.SessionState_SESSION_STATE_CONNECTED
	case sessionapi.StateStopping:
		return grpcproto.SessionState_SESSION_STATE_STOPPING
	case sessionapi.StateFailed:
		return grpcproto.SessionState_SESSION_STATE_FAILED
	default:
		panic(fmt.Sprintf("unsupported session state %q", state))
	}
}

func failureCode(code sessionapi.FailureCode) grpcproto.SessionFailureCode {
	switch code {
	case sessionapi.FailureInvalidArgument:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_INVALID_ARGUMENT
	case sessionapi.FailureNotFound:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_NOT_FOUND
	case sessionapi.FailureConflict:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_CONFLICT
	case sessionapi.FailureNotConfigured:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_NOT_CONFIGURED
	case sessionapi.FailureStaleGeneration:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_STALE_GENERATION
	case sessionapi.FailureUnsupported:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_UNSUPPORTED
	case sessionapi.FailureMalformedConfig:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_MALFORMED_CONFIG
	case sessionapi.FailureProbe:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_PROBE_FAILED
	case sessionapi.FailurePlatform:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_PLATFORM_FAILED
	case sessionapi.FailureRuntime:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_RUNTIME_FAILED
	case sessionapi.FailureCanceled:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_CANCELED
	case sessionapi.FailureInternal:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_INTERNAL
	case sessionapi.FailureCleanup:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_CLEANUP_FAILED
	default:
		panic(fmt.Sprintf("unsupported session failure code %q", code))
	}
}

func failure(err error) *grpcproto.SessionFailure {
	if err == nil {
		return nil
	}
	var domain *sessionapi.Error
	if errors.As(err, &domain) {
		return &grpcproto.SessionFailure{Code: failureCode(domain.Code), Message: domain.Message}
	}
	return &grpcproto.SessionFailure{Code: grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_INTERNAL, Message: "internal session error"}
}

func sourceKind(kind sessionapi.ConfigSourceKind) grpcproto.SessionSourceKind {
	switch kind {
	case sessionapi.ConfigSourceInline:
		return grpcproto.SessionSourceKind_SESSION_SOURCE_KIND_INLINE
	case sessionapi.ConfigSourceURL:
		return grpcproto.SessionSourceKind_SESSION_SOURCE_KIND_URL
	default:
		return grpcproto.SessionSourceKind_SESSION_SOURCE_KIND_UNSPECIFIED
	}
}

func profile(in sessionapi.ProfileSummary) *grpcproto.SessionProfile {
	return &grpcproto.SessionProfile{Index: in.Index, Protocol: protocol(in.Protocol), Description: in.Description}
}

func profiles(in []sessionapi.ProfileSummary) []*grpcproto.SessionProfile {
	out := make([]*grpcproto.SessionProfile, 0, len(in))
	for _, item := range in {
		out = append(out, profile(item))
	}
	return out
}

func warnings(in []sessionapi.Warning) []*grpcproto.SessionWarning {
	out := make([]*grpcproto.SessionWarning, 0, len(in))
	for _, warning := range in {
		out = append(out, &grpcproto.SessionWarning{Code: warning.Code, Message: warning.Message})
	}
	return out
}

func snapshot(in sessionapi.SnapshotResult) *grpcproto.SessionSnapshot {
	out := &grpcproto.SessionSnapshot{
		SessionId: in.SessionID, Sequence: in.Sequence, Generation: in.Generation,
		State: state(in.State), Configured: in.Configured, Digest: in.Digest,
		SourceKind: sourceKind(in.SourceKind), Profiles: profiles(in.Profiles),
		Warnings: warnings(in.Warnings), CleanupComplete: in.CleanupComplete,
		Recovering: in.Recovering,
	}
	if in.ActiveProfile != nil {
		out.ActiveProfile = profile(*in.ActiveProfile)
	}
	if in.LastFailure != "" {
		out.LastFailure = &grpcproto.SessionFailure{Code: failureCode(in.LastFailure), Message: in.LastFailureMessage}
	}
	return out
}
