//go:build !(android || ios)

package proto

import (
	"context"

	"go_module/desktop_exports/common"
	"go_module/grpcproto"
	"go_module/sessionapi/v1"

	"go_module/log"
)

func (s *Server) GetOutlineLastError(_ context.Context, in *grpcproto.Empty) (*grpcproto.GetOutlineLastErrorResponse, error) {
	log.Debugf(common.Category, "GetOutlineLastError")
	err := s.sessionHost().binding.LegacyLastFailure(context.Background())
	return &grpcproto.GetOutlineLastErrorResponse{Error: err}, nil
}

func (s *Server) StartOutline(ctx context.Context, in *grpcproto.StartOutlineRequest) (*grpcproto.StartOutlineResponse, error) {
	log.Debugf(common.Category, "StartOutline")
	result := int32(0)
	if err := s.sessionHost().binding.StartLegacy(ctx, v1.ProtocolOutline, in.GetConfig()); err != nil {
		result = -1
	}
	return &grpcproto.StartOutlineResponse{Result: result}, nil
}

func (s *Server) StopOutline(ctx context.Context, in *grpcproto.Empty) (*grpcproto.Empty, error) {
	log.Debugf(common.Category, "StopOutline")
	_ = s.sessionHost().binding.StopLegacy(ctx)
	return &grpcproto.Empty{}, nil
}
