//go:build !(android || ios)

package proto

import (
	"context"

	"go_module/grpcproto"
	"go_module/sessionapi/desktopbinding"
	"go_module/sessionapi/grpctransport"
	v2 "go_module/sessionapi/v2"
)

type sessionHost struct {
	binding   *desktopbinding.Binding
	transport *grpctransport.Handler
}

func newSessionHost(manager *v2.Manager) *sessionHost {
	binding := desktopbinding.New(manager)
	return &sessionHost{binding: binding, transport: grpctransport.New(binding.Manager)}
}

// The zero-value Server and direct legacy API exports share this exact
// Binding, including its compatibility session ID and serialization lock.
var defaultSessionHost = func() *sessionHost {
	binding := desktopbinding.Default()
	return &sessionHost{binding: binding, transport: grpctransport.New(binding.Manager)}
}()

func (s *Server) sessionHost() *sessionHost {
	if s != nil && s.sessions != nil {
		return s.sessions
	}
	return defaultSessionHost
}

func (s *Server) GetCapabilities(ctx context.Context, in *grpcproto.SessionGetCapabilitiesRequest) (*grpcproto.SessionGetCapabilitiesResponse, error) {
	return s.sessionHost().transport.GetCapabilities(ctx, in)
}
func (s *Server) CreateSession(ctx context.Context, in *grpcproto.SessionCreateSessionRequest) (*grpcproto.SessionCreateSessionResponse, error) {
	return s.sessionHost().transport.CreateSession(ctx, in)
}
func (s *Server) RecoverActiveSession(ctx context.Context, in *grpcproto.Empty) (*grpcproto.SessionRecoverActiveSessionResponse, error) {
	return s.sessionHost().transport.RecoverActiveSession(ctx, in)
}
func (s *Server) Configure(ctx context.Context, in *grpcproto.SessionConfigureRequest) (*grpcproto.SessionConfigureResponse, error) {
	return s.sessionHost().transport.Configure(ctx, in)
}
func (s *Server) Start(ctx context.Context, in *grpcproto.SessionStartRequest) (*grpcproto.SessionStartResponse, error) {
	return s.sessionHost().transport.Start(ctx, in)
}
func (s *Server) Stop(ctx context.Context, in *grpcproto.SessionStopRequest) (*grpcproto.SessionStopResponse, error) {
	return s.sessionHost().transport.Stop(ctx, in)
}
func (s *Server) Snapshot(ctx context.Context, in *grpcproto.SessionSnapshotRequest) (*grpcproto.SessionSnapshotResponse, error) {
	return s.sessionHost().transport.Snapshot(ctx, in)
}
func (s *Server) Observe(ctx context.Context, in *grpcproto.SessionObserveRequest) (*grpcproto.SessionObserveResponse, error) {
	return s.sessionHost().transport.Observe(ctx, in)
}
func (s *Server) Watch(in *grpcproto.SessionObserveRequest, stream grpcproto.Vpn_WatchServer) error {
	return s.sessionHost().transport.Watch(in, stream)
}
func (s *Server) DestroySession(ctx context.Context, in *grpcproto.SessionDestroySessionRequest) (*grpcproto.SessionDestroySessionResponse, error) {
	return s.sessionHost().transport.DestroySession(ctx, in)
}
