//go:build !(android || ios)

package proto

import (
	"context"

	"go_module/desktop_exports/api"
	"go_module/grpcproto"
	"go_module/log"
)

func (s *Server) GetTrustTunnelLastError(_ context.Context, in *grpcproto.Empty) (*grpcproto.GetTrustTunnelLastErrorResponse, error) {
	log.Infof("GetTrustTunnelLastError")
	err := api.GetVpnLastError()
	return &grpcproto.GetTrustTunnelLastErrorResponse{Error: err}, nil
}

func (s *Server) StartTrustTunnel(_ context.Context, in *grpcproto.StartTrustTunnelRequest) (*grpcproto.StartTrustTunnelResponse, error) {
	log.Infof("StartTrustTunnel")
	result := api.StartVpn(in.GetConfig(), "trusttunnel")
	return &grpcproto.StartTrustTunnelResponse{Result: result}, nil
}

func (s *Server) StopTrustTunnel(_ context.Context, in *grpcproto.Empty) (*grpcproto.Empty, error) {
	log.Infof("StopTrustTunnel")
	go api.StopVpn()
	return &grpcproto.Empty{}, nil
}
