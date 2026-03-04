package proto

import (
	"context"

	api "go_client/desktop_exports/api"
	log "go_client/logger"
	protobuf "go_client/vpnserver"
)

func (s *Server) StartCloakClient(_ context.Context, in *protobuf.StartCloakClientRequest) (*protobuf.Empty, error) {
	log.Infof("StartCloakClient")
	go api.StartCloakClient(in.GetLocalHost(), in.GetLocalPort(), in.GetConfig(), in.GetUdp())
	return &protobuf.Empty{}, nil
}

func (s *Server) StopCloakClient(_ context.Context, in *protobuf.Empty) (*protobuf.Empty, error) {
	log.Infof("StopCloakClient")
	go api.StopCloakClient()
	return &protobuf.Empty{}, nil
}
