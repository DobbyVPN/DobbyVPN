// Package grpctransport maps the Go session owner to the desktop gRPC API.
package grpctransport

import (
	"context"
	"fmt"

	"go_module/grpcproto"
	"go_module/sessionapi/desktoptransport"
	v2 "go_module/sessionapi/v2"
)

type Handler struct{ Manager *v2.Manager }

func New(manager *v2.Manager) *Handler { return &Handler{Manager: manager} }

func configureResponse(in v2.ConfigureResult) *grpcproto.SessionConfigureResponse {
	return &grpcproto.SessionConfigureResponse{
		Digest: in.Digest, Sequence: in.Sequence,
		Profiles:   desktoptransport.Profiles(in.Profiles),
		Warnings:   desktoptransport.Warnings(in.Warnings),
		SourceKind: desktoptransport.SourceKind(in.SourceKind),
	}
}

func (h *Handler) ValidateConfig(ctx context.Context, in *grpcproto.SessionValidateConfigRequest) (*grpcproto.SessionValidateConfigResponse, error) {
	result, err := h.Manager.ValidateConfig(ctx, in.GetRawConfig())
	if err != nil {
		return &grpcproto.SessionValidateConfigResponse{Failure: desktoptransport.Failure(err)}, nil
	}
	return &grpcproto.SessionValidateConfigResponse{
		Digest: result.Digest, Profiles: desktoptransport.Profiles(result.Profiles),
		Warnings:   desktoptransport.Warnings(result.Warnings),
		SourceKind: desktoptransport.SourceKind(result.SourceKind),
	}, nil
}

func (h *Handler) Configure(ctx context.Context, in *grpcproto.SessionConfigureRequest) (*grpcproto.SessionConfigureResponse, error) {
	result, err := h.Manager.Configure(ctx, in.GetSessionId(), in.GetExpectedSequence(), in.GetRawConfig())
	if err != nil {
		return &grpcproto.SessionConfigureResponse{Failure: desktoptransport.Failure(err)}, nil
	}
	return configureResponse(result), nil
}

func (h *Handler) Start(ctx context.Context, in *grpcproto.SessionStartRequest) (*grpcproto.SessionStartResponse, error) {
	target, err := startTarget(in.GetMode(), in.GetProfileIndex())
	if err != nil {
		return &grpcproto.SessionStartResponse{Failure: desktoptransport.Failure(err)}, nil
	}
	result, err := h.Manager.Start(ctx, in.GetSessionId(), in.GetExpectedSequence(), target)
	if err != nil {
		return &grpcproto.SessionStartResponse{Failure: desktoptransport.Failure(err)}, nil
	}
	return &grpcproto.SessionStartResponse{Generation: result.Generation, Sequence: result.Sequence}, nil
}

func (h *Handler) Stop(ctx context.Context, in *grpcproto.SessionStopRequest) (*grpcproto.SessionStopResponse, error) {
	result, err := h.Manager.Stop(ctx, in.GetSessionId(), in.GetGeneration())
	if err != nil {
		return &grpcproto.SessionStopResponse{Failure: desktoptransport.Failure(err)}, nil
	}
	return &grpcproto.SessionStopResponse{Generation: result.Generation, Sequence: result.Sequence}, nil
}

func (h *Handler) Snapshot(ctx context.Context, in *grpcproto.SessionSnapshotRequest) (*grpcproto.SessionSnapshotResponse, error) {
	result, err := h.Manager.Snapshot(ctx, in.GetSessionId())
	if err != nil {
		return &grpcproto.SessionSnapshotResponse{Failure: desktoptransport.Failure(err)}, nil
	}
	return &grpcproto.SessionSnapshotResponse{Snapshot: desktoptransport.Snapshot(result)}, nil
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
		case snapshot, ok := <-updates:
			if !ok {
				return nil
			}
			if err := stream.Send(desktoptransport.Snapshot(snapshot)); err != nil {
				return err
			}
		}
	}
}

func (h *Handler) Reset(ctx context.Context, in *grpcproto.SessionResetRequest) (*grpcproto.SessionResetResponse, error) {
	result, err := h.Manager.Reset(ctx, in.GetSessionId(), in.GetExpectedSequence())
	if err != nil {
		return &grpcproto.SessionResetResponse{Failure: desktoptransport.Failure(err)}, nil
	}
	return &grpcproto.SessionResetResponse{Snapshot: desktoptransport.Snapshot(result)}, nil
}

func startTarget(mode grpcproto.SessionStartMode, index int32) (v2.StartTarget, error) {
	switch mode {
	case grpcproto.SessionStartMode_SESSION_START_MODE_AUTO_SELECT:
		return v2.StartTarget{Mode: v2.AutoSelect}, nil
	case grpcproto.SessionStartMode_SESSION_START_MODE_PROFILE_INDEX:
		return v2.StartTarget{Mode: v2.ProfileIndex, Index: int(index)}, nil
	case grpcproto.SessionStartMode_SESSION_START_MODE_UNSPECIFIED:
		return v2.StartTarget{}, &v2.Error{Code: v2.FailureInvalidArgument, Message: "start mode must be AUTO_SELECT or PROFILE_INDEX"}
	default:
		return v2.StartTarget{}, &v2.Error{Code: v2.FailureInvalidArgument, Message: fmt.Sprintf("unrecognized start mode %q", mode)}
	}
}
