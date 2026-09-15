//go:build !(android || ios)

package proto

import (
	"context"
	"sync"

	"go_module/grpcproto"
	"go_module/sessionapi"
	"go_module/sessionapi/grpctransport"
	"go_module/sessionapi/runtimebridge"
)

type desktopPlatform struct{}

func (desktopPlatform) PrepareTunnel(context.Context, sessionapi.SessionRef) (sessionapi.PlatformLease, error) {
	return desktopPlatformLease{}, nil
}
func (desktopPlatform) ProtectSocket(context.Context, sessionapi.SessionRef, int) error { return nil }
func (desktopPlatform) PublishState(context.Context, sessionapi.StateChange)            {}

type desktopPlatformLease struct{}

func (desktopPlatformLease) Release(context.Context) error { return nil }

var defaultSessionHandlerOnce sync.Once
var defaultSessionHandler *grpctransport.Handler

// processSessionHandler lazily constructs the one session manager owned by the
// desktop service process. Tests can inject a native-free manager through
// NewServer without touching this composition root.
func processSessionHandler() *grpctransport.Handler {
	defaultSessionHandlerOnce.Do(func() {
		platform := desktopPlatform{}
		manager := sessionapi.NewManager(sessionapi.ManagerOptions{
			Runtime:  runtimebridge.New(nil),
			Platform: platform,
		})
		defaultSessionHandler = grpctransport.New(manager)
	})
	return defaultSessionHandler
}

func (s *Server) sessionHandler() *grpctransport.Handler {
	if s != nil && s.sessions != nil {
		return s.sessions
	}
	return processSessionHandler()
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
