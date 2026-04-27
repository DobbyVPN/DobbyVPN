package proto

import (
	"context"

	"go_module/desktop_exports/api"
	"go_module/grpcproto"

	"go_module/log"
)

func (c *Server) InitLogger(_ context.Context, in *grpcproto.InitLoggerRequest) (*grpcproto.Empty, error) {
	log.Debugf(Category, "InitLogger")
	go api.InitLogger(in.Path)
	return &grpcproto.Empty{}, nil
}

func (c *Server) InitTelemetry(_ context.Context, in *grpcproto.InitTelemetryRequest) (*grpcproto.Empty, error) {
	log.Debugf(Category, "InitTelemetry")
	go api.InitTelemetry(in.Endpoint)
	return &grpcproto.Empty{}, nil
}
