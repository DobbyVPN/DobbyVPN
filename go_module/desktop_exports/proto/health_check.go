//go:build !(android || ios)

package proto

import (
	"context"

	"go_module/desktop_exports/api"
	"go_module/grpcproto"

	"go_module/log"
)

func (s *Server) CouldStart(_ context.Context, in *grpcproto.Empty) (*grpcproto.CouldStartResponce, error) {
	log.Debugf(Category, "[GRPC] CouldStart")
	result := api.CouldStart()
	log.Debugf(Category, "[GRPC] CouldStart result: %v", result)

	return &grpcproto.CouldStartResponce{Result: result}, nil
}

func (s *Server) GetConnectionState(_ context.Context, in *grpcproto.Empty) (*grpcproto.GetConnectionStateResponce, error) {
	log.Debugf(Category, "[GRPC] GetConnectionState")
	result := api.GetConnectionState()
	log.Debugf(Category, "[GRPC] GetConnectionState result: %v", result)

	return &grpcproto.GetConnectionStateResponce{
		ConnectionState: result,
	}, nil
}

func (s *Server) StartHealthCheck(_ context.Context, in *grpcproto.Empty) (*grpcproto.Empty, error) {
	log.Debugf(Category, "[GRPC] StartHealthCheck")
	api.StartHealthCheck()
	log.Debugf(Category, "[GRPC] StartHealthCheck completed")

	return &grpcproto.Empty{}, nil
}

func (s *Server) InitHealthCheck(_ context.Context, in *grpcproto.Empty) (*grpcproto.Empty, error) {
	log.Debugf(Category, "[GRPC] InitHealthCheck")
	api.InitHealthCheck()
	log.Debugf(Category, "[GRPC] InitHealthCheck completed")

	return &grpcproto.Empty{}, nil
}

func (s *Server) StopHealthCheck(_ context.Context, in *grpcproto.Empty) (*grpcproto.Empty, error) {
	log.Debugf(Category, "[GRPC] StopHealthCheck")
	api.StopHealthCheck()
	log.Debugf(Category, "[GRPC] StopHealthCheck completed")

	return &grpcproto.Empty{}, nil
}
