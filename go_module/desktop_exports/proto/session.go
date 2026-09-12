//go:build !(android || ios)

package proto

import (
	"context"

	"go_module/grpcproto"
	"go_module/sessionapi/desktopbinding"
	"go_module/sessionapi/grpctransport"
)

// The zero-value Server uses this exact SessionV2 binding, including its
// process-local session manager and serialization lock.
var defaultSessionHandler = grpctransport.New(desktopbinding.Default())

func (s *Server) sessionHandler() *grpctransport.Handler {
	if s != nil && s.sessions != nil {
		return s.sessions
	}
	return defaultSessionHandler
}

func (s *Server) GetCapabilities(ctx context.Context, in *grpcproto.SessionGetCapabilitiesRequest) (*grpcproto.SessionGetCapabilitiesResponse, error) {
	return s.sessionHandler().GetCapabilities(ctx, in)
}
func (s *Server) CreateSession(ctx context.Context, in *grpcproto.SessionCreateSessionRequest) (*grpcproto.SessionCreateSessionResponse, error) {
	return s.sessionHandler().CreateSession(ctx, in)
}
func (s *Server) RecoverActiveSession(ctx context.Context, in *grpcproto.Empty) (*grpcproto.SessionRecoverActiveSessionResponse, error) {
	return s.sessionHandler().RecoverActiveSession(ctx, in)
}
func (s *Server) Configure(ctx context.Context, in *grpcproto.SessionConfigureRequest) (*grpcproto.SessionConfigureResponse, error) {
	return s.sessionHandler().Configure(ctx, in)
}
func (s *Server) Start(ctx context.Context, in *grpcproto.SessionStartRequest) (*grpcproto.SessionStartResponse, error) {
	return s.sessionHandler().Start(ctx, in)
}
func (s *Server) Stop(ctx context.Context, in *grpcproto.SessionStopRequest) (*grpcproto.SessionStopResponse, error) {
	return s.sessionHandler().Stop(ctx, in)
}
func (s *Server) Snapshot(ctx context.Context, in *grpcproto.SessionSnapshotRequest) (*grpcproto.SessionSnapshotResponse, error) {
	return s.sessionHandler().Snapshot(ctx, in)
}
func (s *Server) Observe(ctx context.Context, in *grpcproto.SessionObserveRequest) (*grpcproto.SessionObserveResponse, error) {
	return s.sessionHandler().Observe(ctx, in)
}
func (s *Server) Watch(in *grpcproto.SessionObserveRequest, stream grpcproto.Vpn_WatchServer) error {
	return s.sessionHandler().Watch(in, stream)
}
func (s *Server) DestroySession(ctx context.Context, in *grpcproto.SessionDestroySessionRequest) (*grpcproto.SessionDestroySessionResponse, error) {
	return s.sessionHandler().DestroySession(ctx, in)
}
