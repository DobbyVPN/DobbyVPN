package proto

import (
	"context"

	"go_module/desktop_exports/api"
	"go_module/grpcproto"

	"go_module/log"
)

func (s *Server) CouldStart(_ context.Context, in *grpcproto.Empty) (*grpcproto.CouldStartResponce, error) {
	log.Infof("[GRPC] CouldStart")
	result := api.CouldStart()
	log.Infof("[GRPC] CouldStart result: %v", result)

	return &grpcproto.CouldStartResponce{Result: result}, nil
}

func (s *Server) GetConnectionState(_ context.Context, in *grpcproto.Empty) (*grpcproto.GetConnectionStateResponce, error) {
	log.Infof("[GRPC] GetConnectionState")
	result := api.GetConnectionState()
	log.Infof("[GRPC] GetConnectionState result: %v", result)

	return &grpcproto.GetConnectionStateResponce{
		ConnectionState: result,
	}, nil
}

func (s *Server) StartHealthCheck(_ context.Context, in *grpcproto.Empty) (*grpcproto.Empty, error) {
	log.Infof("[GRPC] StartHealthCheck")
	api.StartHealthCheck()
	log.Infof("[GRPC] StartHealthCheck completed")

	return &grpcproto.Empty{}, nil
}

func (s *Server) StopHealthCheck(_ context.Context, in *grpcproto.Empty) (*grpcproto.Empty, error) {
	log.Infof("[GRPC] StopHealthCheck")
	api.StopHealthCheck()
	log.Infof("[GRPC] StopHealthCheck completed")

	return &grpcproto.Empty{}, nil
}
