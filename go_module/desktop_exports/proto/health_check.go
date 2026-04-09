//go:build !(android || ios)

package proto

import (
	"context"

	"go_module/desktop_exports/api"
	"go_module/grpcproto"

	"go_module/log"
)

func (s *Server) CouldStart(_ context.Context, in *grpcproto.Empty) (*grpcproto.CouldStartResponce, error) {
	log.Infof("CouldStart")
	result := api.CouldStart()
	log.Infof("CouldStart: %v", result)
	return &grpcproto.CouldStartResponce{Result: result}, nil
}

func (s *Server) CheckServerAlive(_ context.Context, in *grpcproto.CheckServerAliveRequest) (*grpcproto.CheckServerAliveResponce, error) {
	log.Infof("CheckServerAlive")
	result := api.CheckServerAlive(in.GetAddress(), int(in.GetPort()))
	return &grpcproto.CheckServerAliveResponce{Result: result}, nil
}
