package proto

import (
	"context"

	"go_client/desktop_exports/api"
	"go_client/grpcproto"

	"go_client/log"
)

func (s *Server) StartAwg(_ context.Context, in *grpcproto.StartAwgRequest) (*grpcproto.Empty, error) {
	log.Infof("StartAwg: %v", in.GetTunnel())
	go api.StartAwg(in.GetTunnel(), in.GetConfig())
	return &grpcproto.Empty{}, nil
}

func (s *Server) StopAwg(_ context.Context, in *grpcproto.Empty) (*grpcproto.Empty, error) {
	log.Infof("StopAwg")
	go api.StopAwg()
	return &grpcproto.Empty{}, nil
}
