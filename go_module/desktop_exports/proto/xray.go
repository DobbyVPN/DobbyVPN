package proto

import (
	"context"

	"go_module/desktop_exports/common"
	"go_module/grpcproto"
	"go_module/log"
	"go_module/sessionapi/v1"
)

func (s *Server) GetXrayLastError(_ context.Context, in *grpcproto.Empty) (*grpcproto.GetXrayLastErrorResponse, error) {
	log.Debugf(common.Category, "GetXrayLastError")
	err := s.sessionHost().binding.LegacyLastFailure(context.Background())
	return &grpcproto.GetXrayLastErrorResponse{Error: err}, nil
}

func (s *Server) StartXray(ctx context.Context, in *grpcproto.StartXrayRequest) (*grpcproto.StartXrayResponse, error) {
	log.Debugf(common.Category, "StartXray")
	result := int32(0)
	if err := s.sessionHost().binding.StartLegacy(ctx, v1.ProtocolXray, in.GetConfig()); err != nil {
		result = -1
	}
	return &grpcproto.StartXrayResponse{Result: result}, nil
}

func (s *Server) StopXray(ctx context.Context, in *grpcproto.Empty) (*grpcproto.Empty, error) {
	log.Debugf(common.Category, "StopXray")
	_ = s.sessionHost().binding.StopLegacy(ctx)
	return &grpcproto.Empty{}, nil
}
