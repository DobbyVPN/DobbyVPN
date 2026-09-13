//go:build !(android || ios)

package proto

import (
	"context"

	"go_module/grpcproto"
	"go_module/sessionapi/desktopbinding"
	"go_module/sessionapi/grpctransport"
)

// The zero-value Server uses this exact process-owned session API binding.
var defaultSessionHandler = grpctransport.New(desktopbinding.Default())

func (s *Server) sessionHandler() *grpctransport.Handler {
	if s != nil && s.sessions != nil {
		return s.sessions
	}
	return defaultSessionHandler
}

func (s *Server) ValidateConfig(ctx context.Context, in *grpcproto.SessionValidateConfigRequest) (*grpcproto.SessionValidateConfigResponse, error) {
	return s.sessionHandler().ValidateConfig(ctx, in)
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
func (s *Server) Watch(in *grpcproto.SessionSnapshotRequest, stream grpcproto.Vpn_WatchServer) error {
	return s.sessionHandler().Watch(in, stream)
}
func (s *Server) Reset(ctx context.Context, in *grpcproto.SessionResetRequest) (*grpcproto.SessionResetResponse, error) {
	return s.sessionHandler().Reset(ctx, in)
}
