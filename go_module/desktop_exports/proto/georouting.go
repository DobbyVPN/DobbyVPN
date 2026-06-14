//go:build !(android || ios)

package proto

import (
	"context"

	"go_module/desktop_exports/api"
	"go_module/grpcproto"
)

func (s *Server) SetGeoRoutingConf(_ context.Context, in *grpcproto.SetGeoRoutingConfRequest) (*grpcproto.Empty, error) {
	api.SetGeoRoutingConf(in.GetCidrs())
	return &grpcproto.Empty{}, nil
}

func (s *Server) ClearGeoRoutingConf(_ context.Context, in *grpcproto.Empty) (*grpcproto.Empty, error) {
	api.ClearGeoRoutingConf()
	return &grpcproto.Empty{}, nil
}
