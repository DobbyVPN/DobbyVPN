package proto

import (
	"context"

	"go_module/desktop_exports/api"
	"go_module/grpcproto"

	"go_module/log"
)

func (s *Server) StartCloakClient(_ context.Context, in *grpcproto.StartCloakClientRequest) (*grpcproto.Empty, error) {
	log.Infof("StartCloakClient")
	go api.StartCloakClient(in.GetLocalHost(), in.GetLocalPort(), in.GetConfig(), in.GetUdp())
	return &grpcproto.Empty{}, nil
}

func (s *Server) StopCloakClient(_ context.Context, in *grpcproto.Empty) (*grpcproto.Empty, error) {
	log.Infof("StopCloakClient")
	go api.StopCloakClient()
	return &grpcproto.Empty{}, nil
}
