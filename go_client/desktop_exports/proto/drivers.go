package proto

import (
	"context"

	"go_client/desktop_exports/api"
	"go_client/grpcproto"

	log "go_client/logger"
)

func (c *Server) AddTapDevice(_ context.Context, in *grpcproto.AddTapDeviceRequest) (*grpcproto.Empty, error) {
	log.Infof("AddTapDevice")

	api.AddTapDevice(in.AppDir)

	return &grpcproto.Empty{}, nil
}
