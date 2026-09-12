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
