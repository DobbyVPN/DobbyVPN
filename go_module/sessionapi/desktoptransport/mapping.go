// Package desktoptransport contains native-free session response conversion.
// Desktop gRPC owns construction of the native runtime; this package is kept
// free of protocol/device imports so its safety and ordering tests run on a
// normal Go toolchain.
package desktoptransport

import (
	"errors"

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
		return grpcproto.SessionProtocol_SESSION_PROTOCOL_UNSPECIFIED
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
	case v2.StateDestroyed:
		return grpcproto.SessionState_SESSION_STATE_DESTROYED
	default:
		return grpcproto.SessionState_SESSION_STATE_UNSPECIFIED
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
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_UNSPECIFIED
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
	return &grpcproto.SessionFailure{Code: grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_INTERNAL, Message: "operation failed"}
}
