package proto

import (
	"context"

	api "go_client/desktop_exports/api"
	log "go_client/logger"
	protobuf "go_client/vpnserver"
)

func (s *Server) GetOutlineLastError(_ context.Context, in *protobuf.Empty) (*protobuf.GetOutlineLastErrorResponse, error) {
	log.Infof("GetOutlineLastError")
	err := api.GetOutlineLastError()
	return &protobuf.GetOutlineLastErrorResponse{Error: err}, nil
}

func (s *Server) StartOutline(_ context.Context, in *protobuf.StartOutlineRequest) (*protobuf.StartOutlineResponse, error) {
	log.Infof("StartOutline")
	result := api.StartOutline(in.GetConfig())
	return &protobuf.StartOutlineResponse{Result: result}, nil
}

func (s *Server) StopOutline(_ context.Context, in *protobuf.Empty) (*protobuf.Empty, error) {
	log.Infof("StopOutline")
	go api.StopOutline()
	return &protobuf.Empty{}, nil
}
