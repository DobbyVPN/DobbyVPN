package proto

import (
	"context"

	"go_module/desktop_exports/api"
	"go_module/desktop_exports/common"
	"go_module/grpcproto"
	"go_module/log"
)

func (s *Server) GetXrayLastError(_ context.Context, in *grpcproto.Empty) (*grpcproto.GetXrayLastErrorResponse, error) {
	log.Debugf(common.Category, "GetXrayLastError")
	err := api.GetVpnLastError()
	return &grpcproto.GetXrayLastErrorResponse{Error: err}, nil
}

func (s *Server) StartXray(_ context.Context, in *grpcproto.StartXrayRequest) (*grpcproto.StartXrayResponse, error) {
	log.Debugf(common.Category, "StartXray")
	result := api.StartVpn(in.GetConfig(), "xray")
	return &grpcproto.StartXrayResponse{Result: result}, nil
}

func (s *Server) StopXray(_ context.Context, in *grpcproto.Empty) (*grpcproto.Empty, error) {
	log.Debugf(common.Category, "StopXray")
	api.StopVpn()
	return &grpcproto.Empty{}, nil
}
