package proto

import (
	"context"

	"go_module/desktop_exports/api"
	"go_module/grpcproto"

	"go_module/log"
)

func (s *Server) GetOutlineLastError(_ context.Context, in *grpcproto.Empty) (*grpcproto.GetOutlineLastErrorResponse, error) {
	log.Debugf(Category, "GetOutlineLastError")
	err := api.GetOutlineLastError()
	return &grpcproto.GetOutlineLastErrorResponse{Error: err}, nil
}

func (s *Server) StartOutline(_ context.Context, in *grpcproto.StartOutlineRequest) (*grpcproto.StartOutlineResponse, error) {
	log.Debugf(Category, "StartOutline")
	result := api.StartOutline(in.GetConfig())
	return &grpcproto.StartOutlineResponse{Result: result}, nil
}

func (s *Server) StopOutline(_ context.Context, in *grpcproto.Empty) (*grpcproto.Empty, error) {
	log.Debugf(Category, "StopOutline")
	go api.StopOutline()
	return &grpcproto.Empty{}, nil
}
