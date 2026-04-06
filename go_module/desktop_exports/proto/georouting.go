package proto

import (
	"context"

	"go_module/desktop_exports/api"
	"go_module/grpcproto"

	"go_module/log"
)

func (s *Server) SetGeoRoutingConf(_ context.Context, in *grpcproto.SetGeoRoutingConfRequest) (*grpcproto.Empty, error) {
	log.Infof("SetGeoRoutingConf: %v", in.GetCidrs())
	api.SetGeoRoutingConf(in.GetCidrs())
	return &grpcproto.Empty{}, nil
}

func (s *Server) ClearGeoRoutingConf(_ context.Context, in *grpcproto.Empty) (*grpcproto.Empty, error) {
	log.Infof("ClearGeoRoutingConf")
	api.ClearGeoRoutingConf()
	return &grpcproto.Empty{}, nil
}
