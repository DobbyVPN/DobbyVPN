package proto

import (
	"context"

	api "go_client/desktop_exports/api"
	log "go_client/logger"
	protobuf "go_client/vpnserver"
)

func (c *Server) InitLogger(_ context.Context, in *protobuf.InitLoggerRequest) (*protobuf.Empty, error) {
	log.Infof("InitLogger")
	go api.InitLogger(in.Path)
	return &protobuf.Empty{}, nil
}
