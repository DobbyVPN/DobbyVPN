package proto

import (
	"context"
	"fmt"

	"go_module/desktop_exports/api"
	"go_module/grpcproto"

	"go_module/log"
)

func (s *Server) NetCheck(_ context.Context, in *grpcproto.NetCheckRequest) (*grpcproto.NetCheckResponse, error) {
	log.Infof("NetCheck")
	err := api.NetCheck(in.GetConfigPath())
	if err != nil {
		return &grpcproto.NetCheckResponse{Error: fmt.Sprintf("NetCheck error: %v", err)}, nil
	} else {
		return &grpcproto.NetCheckResponse{Error: ""}, nil
	}
}

func (s *Server) CancelNetCheck(_ context.Context, in *grpcproto.Empty) (*grpcproto.Empty, error) {
	log.Infof("CancelNetCheck")
	api.CancelNetCheck()
	return &grpcproto.Empty{}, nil
}
