//go:build !(android || ios)

package proto

import (
	"context"

	"go_module/desktop_exports/api"
	"go_module/grpcproto"

	"go_module/log"
)

func (c *Server) InitLogger(_ context.Context, in *grpcproto.InitLoggerRequest) (*grpcproto.Empty, error) {
	log.Debugf(Category, "InitLogger")
	api.InitLogger(in.Path)
	return &grpcproto.Empty{}, nil
}

func (c *Server) InitTelemetry(_ context.Context, in *grpcproto.InitTelemetryRequest) (*grpcproto.Empty, error) {
	log.Debugf(Category, "InitTelemetry")
	go api.InitTelemetry(in.Endpoint)
	return &grpcproto.Empty{}, nil
}

func (c *Server) StopTelemetry(_ context.Context, in *grpcproto.Empty) (*grpcproto.Empty, error) {
	log.Debugf(Category, "StopTelemetry")
	go api.StopTelemetry()
	return &grpcproto.Empty{}, nil
}

func (c *Server) SetupTelemetryAttributes(_ context.Context, in *grpcproto.SetupTelemetryAttributesRequest) (*grpcproto.Empty, error) {
	log.Debugf(Category, "SetupTelemetryAttributes")
	go api.SetupTelemetryAttributes(in.Config)
	return &grpcproto.Empty{}, nil
}
