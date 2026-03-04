package proto

import (
	"context"

	"go_client/desktop_exports/api"
	"go_client/grpcproto"

	log "go_client/logger"
)

func (s *Server) GetOutlineLastError(_ context.Context, in *grpcproto.Empty) (*grpcproto.GetOutlineLastErrorResponse, error) {
	log.Infof("GetOutlineLastError")
	err := api.GetOutlineLastError()
	return &grpcproto.GetOutlineLastErrorResponse{Error: err}, nil
}

func (s *Server) StartOutline(_ context.Context, in *grpcproto.StartOutlineRequest) (*grpcproto.StartOutlineResponse, error) {
	log.Infof("StartOutline")
	result := api.StartOutline(in.GetConfig())
	return &grpcproto.StartOutlineResponse{Result: result}, nil
}

func (s *Server) StopOutline(_ context.Context, in *grpcproto.Empty) (*grpcproto.Empty, error) {
	log.Infof("StopOutline")
	go api.StopOutline()
	return &grpcproto.Empty{}, nil
}
