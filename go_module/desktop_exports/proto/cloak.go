//go:build !(android || ios)

package proto

import (
	"context"

	"go_module/desktop_exports/api"
	"go_module/desktop_exports/common"
	"go_module/grpcproto"

	"go_module/log"
)

func (s *Server) StartCloakClient(_ context.Context, in *grpcproto.StartCloakClientRequest) (*grpcproto.Empty, error) {
	log.Debugf(common.Category, "StartCloakClient")
	if err := api.StartCloakClient(in.GetLocalHost(), in.GetLocalPort(), in.GetConfig(), in.GetUdp()); err != nil {
		log.Debugf(common.Category, "StartCloakClient failed: %v", err)
		return nil, err
	}
	return &grpcproto.Empty{}, nil
}

func (s *Server) StopCloakClient(_ context.Context, in *grpcproto.Empty) (*grpcproto.Empty, error) {
	go api.StopCloakClient()
	return &grpcproto.Empty{}, nil
}
