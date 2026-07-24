// Package grpctransport is the native-free gRPC representation of sessionapi.
// Desktop bindings construct the manager/runtime separately and delegate all
// v1 RPCs here so response mapping remains independently testable.
package grpctransport

import (
	"context"

	"go_module/grpcproto"
	"go_module/sessionapi/desktoptransport"
	"go_module/sessionapi/v1"
)

type Handler struct{ Manager *v1.Manager }

func New(manager *v1.Manager) *Handler { return &Handler{Manager: manager} }

func (h *Handler) GetCapabilities(ctx context.Context, _ *grpcproto.SessionGetCapabilitiesRequest) (*grpcproto.SessionGetCapabilitiesResponse, error) {
	capabilities := h.Manager.GetCapabilities(ctx)
	response := &grpcproto.SessionGetCapabilitiesResponse{Version: capabilities.Version, TelemetryNetworkDisabled: capabilities.TelemetryNetworkDisabled, Protocols: make([]grpcproto.SessionProtocol, 0, len(capabilities.Protocols)), Features: make([]*grpcproto.SessionFeature, 0, len(capabilities.Features))}
	for _, protocol := range capabilities.Protocols {
		response.Protocols = append(response.Protocols, desktoptransport.Protocol(protocol))
	}
	for _, feature := range capabilities.Features {
		response.Features = append(response.Features, &grpcproto.SessionFeature{Name: feature.Name, Enabled: feature.Enabled})
	}
	return response, nil
}

func (h *Handler) CreateSession(ctx context.Context, _ *grpcproto.SessionCreateSessionRequest) (*grpcproto.SessionCreateSessionResponse, error) {
	id, err := h.Manager.CreateSession(ctx)
	if err != nil {
		return &grpcproto.SessionCreateSessionResponse{Failure: desktoptransport.Failure(err)}, nil
	}
	return &grpcproto.SessionCreateSessionResponse{SessionId: id}, nil
}

func (h *Handler) Configure(ctx context.Context, in *grpcproto.SessionConfigureRequest) (*grpcproto.SessionConfigureResponse, error) {
	result, err := h.Manager.Configure(ctx, in.GetSessionId(), in.GetCommandId(), in.GetRawConfig())
	if err != nil {
		return &grpcproto.SessionConfigureResponse{Failure: desktoptransport.Failure(err)}, nil
	}
	return &grpcproto.SessionConfigureResponse{Digest: result.Digest, Profiles: profiles(result.Profiles), Warnings: warnings(result.Warnings)}, nil
}

func (h *Handler) Start(ctx context.Context, in *grpcproto.SessionStartRequest) (*grpcproto.SessionStartResponse, error) {
	target, err := startTarget(in.GetMode(), in.GetProfileIndex())
	if err != nil {
		return &grpcproto.SessionStartResponse{Failure: desktoptransport.Failure(err)}, nil
	}
	result, err := h.Manager.Start(ctx, in.GetSessionId(), in.GetCommandId(), target)
	if err != nil {
		return &grpcproto.SessionStartResponse{Failure: desktoptransport.Failure(err)}, nil
	}
	return &grpcproto.SessionStartResponse{Generation: result.Generation}, nil
}

func (h *Handler) Stop(ctx context.Context, in *grpcproto.SessionStopRequest) (*grpcproto.SessionStopResponse, error) {
	result, err := h.Manager.Stop(ctx, in.GetSessionId(), in.GetCommandId(), in.GetGeneration())
	if err != nil {
		return &grpcproto.SessionStopResponse{Failure: desktoptransport.Failure(err)}, nil
	}
	return &grpcproto.SessionStopResponse{Generation: result.Generation}, nil
}

func (h *Handler) Snapshot(ctx context.Context, in *grpcproto.SessionSnapshotRequest) (*grpcproto.SessionSnapshotResponse, error) {
	result, err := h.Manager.Snapshot(ctx, in.GetSessionId())
	if err != nil {
		return &grpcproto.SessionSnapshotResponse{Failure: desktoptransport.Failure(err)}, nil
	}
	return &grpcproto.SessionSnapshotResponse{Snapshot: snapshot(result)}, nil
}

func (h *Handler) Observe(ctx context.Context, in *grpcproto.SessionObserveRequest) (*grpcproto.SessionObserveResponse, error) {
	result, err := h.Manager.Observe(ctx, in.GetSessionId(), in.GetAfterSequence())
	if err != nil {
		return &grpcproto.SessionObserveResponse{Failure: desktoptransport.Failure(err)}, nil
	}
	events := make([]*grpcproto.SessionEvent, 0, len(result.Events))
	for _, event := range result.Events {
		events = append(events, eventResponse(event))
	}
	return &grpcproto.SessionObserveResponse{Events: events, NextSequence: result.NextSequence}, nil
}

func (h *Handler) DestroySession(ctx context.Context, in *grpcproto.SessionDestroySessionRequest) (*grpcproto.SessionDestroySessionResponse, error) {
	if err := h.Manager.DestroySession(ctx, in.GetSessionId()); err != nil {
		return &grpcproto.SessionDestroySessionResponse{Failure: desktoptransport.Failure(err)}, nil
	}
	return &grpcproto.SessionDestroySessionResponse{Destroyed: true}, nil
}

func startTarget(mode grpcproto.SessionStartMode, index int32) (v1.StartTarget, error) {
	switch mode {
	case grpcproto.SessionStartMode_SESSION_START_MODE_AUTO_SELECT:
		return v1.StartTarget{Mode: v1.AutoSelect}, nil
	case grpcproto.SessionStartMode_SESSION_START_MODE_PROFILE_INDEX:
		return v1.StartTarget{Mode: v1.ProfileIndex, Index: int(index)}, nil
	default:
		return v1.StartTarget{}, &v1.Error{Code: v1.FailureInvalidArgument, Message: "start mode must be AUTO_SELECT or PROFILE_INDEX"}
	}
}

func profiles(in []v1.ProfileSummary) []*grpcproto.SessionProfile {
	out := make([]*grpcproto.SessionProfile, 0, len(in))
	for _, item := range in {
		out = append(out, profile(item))
	}
	return out
}
func profile(in v1.ProfileSummary) *grpcproto.SessionProfile {
	return &grpcproto.SessionProfile{Index: int32(in.Index), Protocol: desktoptransport.Protocol(in.Protocol), Description: in.Description}
}
func warnings(in []v1.Warning) []*grpcproto.SessionWarning {
	out := make([]*grpcproto.SessionWarning, 0, len(in))
	for _, item := range in {
		out = append(out, &grpcproto.SessionWarning{Code: item.Code, Message: item.Message})
	}
	return out
}
func eventResponse(in v1.Event) *grpcproto.SessionEvent {
	out := &grpcproto.SessionEvent{SessionId: in.SessionID, Generation: in.Generation, Sequence: in.Sequence, State: desktoptransport.State(in.State)}
	if in.Profile != nil {
		out.Profile = profile(*in.Profile)
	}
	if in.Failure != "" {
		out.Failure = &grpcproto.SessionFailure{Code: desktoptransport.FailureCode(in.Failure)}
	}
	if in.Warning != nil {
		out.Warning = &grpcproto.SessionWarning{Code: in.Warning.Code, Message: in.Warning.Message}
	}
	return out
}
func snapshot(in v1.SnapshotResult) *grpcproto.SessionSnapshot {
	out := &grpcproto.SessionSnapshot{SessionId: in.SessionID, Generation: in.Generation, State: desktoptransport.State(in.State), Configured: in.Configured, CleanupComplete: in.CleanupComplete}
	if in.ActiveProfile != nil {
		out.ActiveProfile = profile(*in.ActiveProfile)
	}
	if in.LastFailure != "" {
		out.LastFailure = &grpcproto.SessionFailure{Code: desktoptransport.FailureCode(in.LastFailure)}
	}
	return out
}
