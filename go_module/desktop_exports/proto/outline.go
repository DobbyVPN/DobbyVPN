//go:build !(android || ios)

package proto

import (
	"context"

	"go_module/desktop_exports/api"
	"go_module/grpcproto"

	"go_module/log"
)

func (s *Server) GetOutlineLastError(_ context.Context, in *grpcproto.Empty) (*grpcproto.GetOutlineLastErrorResponse, error) {
	log.Infof("GetOutlineLastError")
	err := api.GetVpnLastError()
	return &grpcproto.GetOutlineLastErrorResponse{Error: err}, nil
}

func (s *Server) StartOutline(_ context.Context, in *grpcproto.StartOutlineRequest) (*grpcproto.StartOutlineResponse, error) {
	log.Infof("StartOutline")
	result := api.StartVpn(in.GetConfig(), "outline")
	return &grpcproto.StartOutlineResponse{Result: result}, nil
}

func (s *Server) StopOutline(_ context.Context, in *grpcproto.Empty) (*grpcproto.Empty, error) {
	log.Infof("StopOutline")
	go api.StopVpn()
	return &grpcproto.Empty{}, nil
}
