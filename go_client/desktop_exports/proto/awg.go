package proto

import (
	"context"

	api "go_client/desktop_exports/api"
	log "go_client/logger"
	protobuf "go_client/vpnserver"
)

func (s *Server) StartAwg(_ context.Context, in *protobuf.StartAwgRequest) (*protobuf.Empty, error) {
	log.Infof("StartAwg: %v", in.GetTunnel())
	go api.StartAwg(in.GetTunnel(), in.GetConfig())
	return &protobuf.Empty{}, nil
}

func (s *Server) StopAwg(_ context.Context, in *protobuf.Empty) (*protobuf.Empty, error) {
	log.Infof("StopAwg")
	go api.StopAwg()
	return &protobuf.Empty{}, nil
}
