// Package desktoptransport contains native-free session response conversion.
// Desktop gRPC owns construction of the native runtime; this package is kept
// free of protocol/device imports so its safety and ordering tests run on a
// normal Go toolchain.
package desktoptransport

import (
	"errors"

	"go_module/grpcproto"
	v1 "go_module/sessionapi/v1"
)

func Protocol(protocol v1.Protocol) grpcproto.SessionProtocol {
	switch protocol {
	case v1.ProtocolOutline:
		return grpcproto.SessionProtocol_SESSION_PROTOCOL_OUTLINE
	case v1.ProtocolXray:
		return grpcproto.SessionProtocol_SESSION_PROTOCOL_XRAY
	case v1.ProtocolTrustTunnel:
		return grpcproto.SessionProtocol_SESSION_PROTOCOL_TRUST_TUNNEL
	default:
		return grpcproto.SessionProtocol_SESSION_PROTOCOL_UNSPECIFIED
	}
}

func State(state v1.State) grpcproto.SessionState {
	switch state {
	case v1.StateIdle:
		return grpcproto.SessionState_SESSION_STATE_IDLE
	case v1.StateConfigured:
		return grpcproto.SessionState_SESSION_STATE_CONFIGURED
	case v1.StateProbing:
		return grpcproto.SessionState_SESSION_STATE_PROBING
	case v1.StatePreparing:
		return grpcproto.SessionState_SESSION_STATE_PREPARING
	case v1.StateConnected:
		return grpcproto.SessionState_SESSION_STATE_CONNECTED
	case v1.StateStopping:
		return grpcproto.SessionState_SESSION_STATE_STOPPING
	case v1.StateFailed:
		return grpcproto.SessionState_SESSION_STATE_FAILED
	case v1.StateDestroyed:
		return grpcproto.SessionState_SESSION_STATE_DESTROYED
	default:
		return grpcproto.SessionState_SESSION_STATE_UNSPECIFIED
	}
}

func FailureCode(code v1.FailureCode) grpcproto.SessionFailureCode {
	switch code {
	case v1.FailureInvalidArgument:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_INVALID_ARGUMENT
	case v1.FailureNotFound:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_NOT_FOUND
	case v1.FailureConflict:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_CONFLICT
	case v1.FailureNotConfigured:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_NOT_CONFIGURED
	case v1.FailureStaleGeneration:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_STALE_GENERATION
	case v1.FailureUnsupported:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_UNSUPPORTED
	case v1.FailureMalformedConfig:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_MALFORMED_CONFIG
	case v1.FailureProbe:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_PROBE_FAILED
	case v1.FailurePlatform:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_PLATFORM_FAILED
	case v1.FailureRuntime:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_RUNTIME_FAILED
	case v1.FailureCanceled:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_CANCELED
	case v1.FailureInternal:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_INTERNAL
	default:
		return grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_UNSPECIFIED
	}
}

func Failure(err error) *grpcproto.SessionFailure {
	if err == nil {
		return nil
	}
	var domain *v1.Error
	if errors.As(err, &domain) {
		return &grpcproto.SessionFailure{Code: FailureCode(domain.Code), Message: domain.Message}
	}
	return &grpcproto.SessionFailure{Code: grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_INTERNAL, Message: "operation failed"}
}
