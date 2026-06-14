//go:build !(android || ios)

package proto

import (
	"context"

	"go_module/desktop_exports/api"
	"go_module/grpcproto"
)

func (s *Server) StartAwg(_ context.Context, in *grpcproto.StartAwgRequest) (*grpcproto.Empty, error) {
	go api.StartAwg("awg0", in.GetConfig())
	return &grpcproto.Empty{}, nil
}

func (s *Server) StopAwg(_ context.Context, in *grpcproto.Empty) (*grpcproto.Empty, error) {
	go api.StopAwg()
	return &grpcproto.Empty{}, nil
}
