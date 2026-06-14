//go:build !(android || ios)

package proto

import (
	"context"

	"go_module/desktop_exports/api"
	"go_module/desktop_exports/common"
	"go_module/grpcproto"

	"go_module/log"
)

func (s *Server) CouldStart(_ context.Context, in *grpcproto.Empty) (*grpcproto.CouldStartResponce, error) {
	result := api.CouldStart()
	log.Debugf(common.Category, "CouldStart result: %v", result)

	return &grpcproto.CouldStartResponce{Result: result}, nil
}

func (s *Server) GetConnectionState(_ context.Context, in *grpcproto.Empty) (*grpcproto.GetConnectionStateResponce, error) {
	result := api.GetConnectionState()

	return &grpcproto.GetConnectionStateResponce{
		ConnectionState: result,
	}, nil
}

func (s *Server) StartHealthCheck(_ context.Context, in *grpcproto.Empty) (*grpcproto.Empty, error) {
	log.Debugf(common.Category, "StartHealthCheck")
	api.StartHealthCheck()

	return &grpcproto.Empty{}, nil
}

func (s *Server) InitHealthCheck(_ context.Context, in *grpcproto.Empty) (*grpcproto.Empty, error) {
	log.Debugf(common.Category, "InitHealthCheck")
	api.InitHealthCheck()

	return &grpcproto.Empty{}, nil
}

func (s *Server) StopHealthCheck(_ context.Context, in *grpcproto.Empty) (*grpcproto.Empty, error) {
	log.Debugf(common.Category, "StopHealthCheck")
	api.StopHealthCheck()

	return &grpcproto.Empty{}, nil
}
