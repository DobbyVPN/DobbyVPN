//go:build !(android || ios)

package proto

import (
	"context"

	"go_module/desktop_exports/api"
	"go_module/desktop_exports/common"
	"go_module/grpcproto"

	"go_module/log"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (c *Server) InitLogger(_ context.Context, in *grpcproto.InitLoggerRequest) (*grpcproto.Empty, error) {
	log.Debugf(common.Category, "InitLogger")
	if err := api.InitLogger(in.Path); err != nil {
		return nil, status.Error(codes.Internal, "could not initialize local logger")
	}
	return &grpcproto.Empty{}, nil
}

func (c *Server) InitTelemetry(_ context.Context, in *grpcproto.InitTelemetryRequest) (*grpcproto.Empty, error) {
	log.Debugf(common.Category, "InitTelemetry")
	api.InitTelemetry(in.Endpoint, in.Token)
	return &grpcproto.Empty{}, nil
}

func (c *Server) StopTelemetry(_ context.Context, in *grpcproto.Empty) (*grpcproto.Empty, error) {
	log.Debugf(common.Category, "StopTelemetry")
	api.StopTelemetry()
	return &grpcproto.Empty{}, nil
}

func (c *Server) SetupTelemetryAttributes(_ context.Context, in *grpcproto.SetupTelemetryAttributesRequest) (*grpcproto.Empty, error) {
	log.Debugf(common.Category, "SetupTelemetryAttributes")
	api.SetupTelemetryAttributes(in.Config)
	return &grpcproto.Empty{}, nil
}
