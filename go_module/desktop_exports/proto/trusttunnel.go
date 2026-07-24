//go:build !(android || ios)

package proto

import (
	"context"

	"go_module/grpcproto"
	"go_module/log"
	"go_module/sessionapi/v1"
)

func (s *Server) GetTrustTunnelLastError(_ context.Context, in *grpcproto.Empty) (*grpcproto.GetTrustTunnelLastErrorResponse, error) {
	log.Infof("desktop_exports", "GetTrustTunnelLastError")
	err := s.sessionHost().binding.LegacyLastFailure(context.Background())
	return &grpcproto.GetTrustTunnelLastErrorResponse{Error: err}, nil
}

func (s *Server) StartTrustTunnel(ctx context.Context, in *grpcproto.StartTrustTunnelRequest) (*grpcproto.StartTrustTunnelResponse, error) {
	log.Infof("desktop_exports", "StartTrustTunnel")
	result := int32(0)
	if err := s.sessionHost().binding.StartLegacy(ctx, v1.ProtocolTrustTunnel, in.GetConfig()); err != nil {
		result = -1
	}
	return &grpcproto.StartTrustTunnelResponse{Result: result}, nil
}

func (s *Server) StopTrustTunnel(ctx context.Context, in *grpcproto.Empty) (*grpcproto.Empty, error) {
	log.Infof("desktop_exports", "StopTrustTunnel")
	_ = s.sessionHost().binding.StopLegacy(ctx)
	return &grpcproto.Empty{}, nil
}
