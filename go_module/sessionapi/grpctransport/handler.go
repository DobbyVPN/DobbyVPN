// Package grpctransport is the native-free gRPC representation of sessionapi.
// Desktop bindings construct the manager/runtime separately and delegate all
// session RPCs here so response mapping remains independently testable.
package grpctransport

import (
	"context"
	"math"

	"go_module/grpcproto"
	"go_module/sessionapi/desktoptransport"
	v2 "go_module/sessionapi/v2"
)

type Handler struct{ Manager *v2.Manager }

func New(manager *v2.Manager) *Handler { return &Handler{Manager: manager} }

func (h *Handler) GetCapabilities(ctx context.Context, _ *grpcproto.SessionGetCapabilitiesRequest) (*grpcproto.SessionGetCapabilitiesResponse, error) {
	capabilities := h.Manager.GetCapabilities(ctx)
	response := &grpcproto.SessionGetCapabilitiesResponse{Version: capabilities.Version, Protocols: make([]grpcproto.SessionProtocol, 0, len(capabilities.Protocols)), Features: make([]*grpcproto.SessionFeature, 0, len(capabilities.Features))}
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

func (h *Handler) RecoverActiveSession(ctx context.Context, _ *grpcproto.Empty) (*grpcproto.SessionRecoverActiveSessionResponse, error) {
	id, err := h.Manager.RecoverActiveSession(ctx)
	if err != nil {
		return &grpcproto.SessionRecoverActiveSessionResponse{Failure: desktoptransport.Failure(err)}, nil
	}
	return &grpcproto.SessionRecoverActiveSessionResponse{SessionId: id}, nil
}

func (h *Handler) Configure(ctx context.Context, in *grpcproto.SessionConfigureRequest) (*grpcproto.SessionConfigureResponse, error) {
	result, err := h.Manager.Configure(ctx, in.GetSessionId(), in.GetCommandId(), in.GetRawConfig())
	if err != nil {
		return &grpcproto.SessionConfigureResponse{Failure: desktoptransport.Failure(err)}, nil
	}
	return &grpcproto.SessionConfigureResponse{Digest: result.Digest, Profiles: profiles(result.Profiles), Warnings: warnings(result.Warnings), SourceKind: sourceKind(result.SourceKind)}, nil
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

// Watch streams the append-only event ledger from after_sequence onward. The
// manager owns ordering and closes the stream when the session is destroyed.
func (h *Handler) Watch(in *grpcproto.SessionObserveRequest, stream grpcproto.Vpn_WatchServer) error {
	events, closeSubscription, err := h.Manager.Subscribe(stream.Context(), in.GetSessionId(), in.GetAfterSequence())
	if err != nil {
		return err
	}
	defer closeSubscription()
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case event, ok := <-events:
			if !ok {
				return nil
			}
			if err := stream.Send(eventResponse(event)); err != nil {
				return err
			}
		}
	}
}

func (h *Handler) DestroySession(ctx context.Context, in *grpcproto.SessionDestroySessionRequest) (*grpcproto.SessionDestroySessionResponse, error) {
	if err := h.Manager.DestroySession(ctx, in.GetSessionId()); err != nil {
		return &grpcproto.SessionDestroySessionResponse{Failure: desktoptransport.Failure(err)}, nil
	}
	return &grpcproto.SessionDestroySessionResponse{Destroyed: true}, nil
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
		return v2.StartTarget{}, &v2.Error{Code: v2.FailureInvalidArgument, Message: "unrecognized start mode"}
	}
}

func profiles(in []v2.ProfileSummary) []*grpcproto.SessionProfile {
	out := make([]*grpcproto.SessionProfile, 0, len(in))
	for _, item := range in {
		out = append(out, profile(item))
	}
	return out
}
func profile(in v2.ProfileSummary) *grpcproto.SessionProfile {
	index := int32(-1)
	if in.Index >= 0 && in.Index <= math.MaxInt32 {
		index = int32(in.Index) // #nosec G115 -- bounds checked immediately above.
	}
	return &grpcproto.SessionProfile{Index: index, Protocol: desktoptransport.Protocol(in.Protocol), Description: in.Description}
}
func warnings(in []v2.Warning) []*grpcproto.SessionWarning {
	out := make([]*grpcproto.SessionWarning, 0, len(in))
	for _, item := range in {
		out = append(out, &grpcproto.SessionWarning{Code: item.Code, Message: item.Message})
	}
	return out
}

func sourceKind(kind v2.ConfigSourceKind) grpcproto.SessionSourceKind {
	switch kind {
	case v2.ConfigSourceURL:
		return grpcproto.SessionSourceKind_SESSION_SOURCE_KIND_URL
	case v2.ConfigSourceInline:
		return grpcproto.SessionSourceKind_SESSION_SOURCE_KIND_INLINE
	default:
		return grpcproto.SessionSourceKind_SESSION_SOURCE_KIND_UNSPECIFIED
	}
}
func eventResponse(in v2.Event) *grpcproto.SessionEvent {
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
func snapshot(in v2.SnapshotResult) *grpcproto.SessionSnapshot {
	out := &grpcproto.SessionSnapshot{SessionId: in.SessionID, Generation: in.Generation, State: desktoptransport.State(in.State), Configured: in.Configured, CleanupComplete: in.CleanupComplete}
	if in.ActiveProfile != nil {
		out.ActiveProfile = profile(*in.ActiveProfile)
	}
	if in.LastFailure != "" {
		out.LastFailure = &grpcproto.SessionFailure{Code: desktoptransport.FailureCode(in.LastFailure)}
	}
	return out
}
