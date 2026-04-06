package proto

import (
	"context"

	"go_client/desktop_exports/api"
	"go_client/grpcproto"

	"go_client/log"
)

func (c *Server) InitLogger(_ context.Context, in *grpcproto.InitLoggerRequest) (*grpcproto.Empty, error) {
	log.Infof("InitLogger")
	go api.InitLogger(in.Path)
	return &grpcproto.Empty{}, nil
}
