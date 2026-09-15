// Package grpctransport maps the Go session owner to the desktop gRPC API.
package grpctransport

import (
	"context"
	"fmt"

	"go_module/grpcproto"
	"go_module/sessionapi"
)

type Handler struct{ Manager *sessionapi.Manager }

func New(manager *sessionapi.Manager) *Handler { return &Handler{Manager: manager} }

func configureResponse(in sessionapi.ConfigureResult) *grpcproto.SessionConfigureResponse {
	return &grpcproto.SessionConfigureResponse{
		Digest: in.Digest, Sequence: in.Sequence,
		Profiles:   profiles(in.Profiles),
		Warnings:   warnings(in.Warnings),
		SourceKind: sourceKind(in.SourceKind),
	}
}

func (h *Handler) ValidateConfig(ctx context.Context, in *grpcproto.SessionValidateConfigRequest) (*grpcproto.SessionValidateConfigResponse, error) {
	result, err := h.Manager.ValidateConfig(ctx, in.GetRawConfig())
	if err != nil {
		return &grpcproto.SessionValidateConfigResponse{Failure: failure(err)}, nil
	}
	return &grpcproto.SessionValidateConfigResponse{
		Digest: result.Digest, Profiles: profiles(result.Profiles),
		Warnings:   warnings(result.Warnings),
		SourceKind: sourceKind(result.SourceKind),
	}, nil
}

func (h *Handler) Configure(ctx context.Context, in *grpcproto.SessionConfigureRequest) (*grpcproto.SessionConfigureResponse, error) {
	result, err := h.Manager.Configure(ctx, in.GetSessionId(), in.GetExpectedSequence(), in.GetRawConfig())
	if err != nil {
		return &grpcproto.SessionConfigureResponse{Failure: failure(err)}, nil
	}
	return configureResponse(result), nil
}

func (h *Handler) Start(ctx context.Context, in *grpcproto.SessionStartRequest) (*grpcproto.SessionStartResponse, error) {
	target, err := startTarget(in.GetMode(), in.GetProfileIndex())
	if err != nil {
		return &grpcproto.SessionStartResponse{Failure: failure(err)}, nil
	}
	result, err := h.Manager.Start(ctx, in.GetSessionId(), in.GetExpectedSequence(), target)
	if err != nil {
		return &grpcproto.SessionStartResponse{Failure: failure(err)}, nil
	}
	return &grpcproto.SessionStartResponse{Generation: result.Generation, Sequence: result.Sequence}, nil
}

func (h *Handler) Stop(ctx context.Context, in *grpcproto.SessionStopRequest) (*grpcproto.SessionStopResponse, error) {
	result, err := h.Manager.Stop(ctx, in.GetSessionId(), in.GetGeneration())
	if err != nil {
		return &grpcproto.SessionStopResponse{Failure: failure(err)}, nil
	}
	return &grpcproto.SessionStopResponse{Generation: result.Generation, Sequence: result.Sequence}, nil
}

func (h *Handler) Snapshot(ctx context.Context, in *grpcproto.SessionSnapshotRequest) (*grpcproto.SessionSnapshotResponse, error) {
	result, err := h.Manager.Snapshot(ctx, in.GetSessionId())
	if err != nil {
		return &grpcproto.SessionSnapshotResponse{Failure: failure(err)}, nil
	}
	return &grpcproto.SessionSnapshotResponse{Snapshot: snapshot(result)}, nil
}

// Watch sends an immediate snapshot and coalesced current state after changes.
func (h *Handler) Watch(in *grpcproto.SessionSnapshotRequest, stream grpcproto.Vpn_WatchServer) error {
	updates, closeWatch, err := h.Manager.Watch(stream.Context(), in.GetSessionId())
	if err != nil {
		return err
	}
	defer closeWatch()
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case current, ok := <-updates:
			if !ok {
				return nil
			}
			if err := stream.Send(snapshot(current)); err != nil {
				return err
			}
		}
	}
}

func (h *Handler) Reset(ctx context.Context, in *grpcproto.SessionResetRequest) (*grpcproto.SessionResetResponse, error) {
	result, err := h.Manager.Reset(ctx, in.GetSessionId(), in.GetExpectedSequence())
	if err != nil {
		return &grpcproto.SessionResetResponse{Failure: failure(err)}, nil
	}
	return &grpcproto.SessionResetResponse{Snapshot: snapshot(result)}, nil
}

func startTarget(mode grpcproto.SessionStartMode, index int32) (sessionapi.StartTarget, error) {
	switch mode {
	case grpcproto.SessionStartMode_SESSION_START_MODE_AUTO_SELECT:
		return sessionapi.StartTarget{Mode: sessionapi.AutoSelect}, nil
	case grpcproto.SessionStartMode_SESSION_START_MODE_PROFILE_INDEX:
		return sessionapi.StartTarget{Mode: sessionapi.ProfileIndex, Index: int(index)}, nil
	case grpcproto.SessionStartMode_SESSION_START_MODE_UNSPECIFIED:
		return sessionapi.StartTarget{}, &sessionapi.Error{Code: sessionapi.FailureInvalidArgument, Message: "start mode must be AUTO_SELECT or PROFILE_INDEX"}
	default:
		return sessionapi.StartTarget{}, &sessionapi.Error{Code: sessionapi.FailureInvalidArgument, Message: fmt.Sprintf("unrecognized start mode %q", mode)}
	}
}
