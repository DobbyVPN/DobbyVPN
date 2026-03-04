package proto

import (
	"context"
	"fmt"

	"go_client/desktop_exports/api"
	"go_client/grpcproto"

	log "go_client/logger"
)

func (s *Server) StartHealthCheck(_ context.Context, in *grpcproto.StartHealthCheckRequest) (*grpcproto.Empty, error) {
	log.Infof("StartHealthCheck")
	go api.StartHealthCheck(in.GetPeriod(), in.GetSendMetrics())
	return &grpcproto.Empty{}, nil
}

func (s *Server) StopHealthCheck(_ context.Context, in *grpcproto.Empty) (*grpcproto.Empty, error) {
	log.Infof("StopHealthCheck")
	go api.StopHealthCheck()
	return &grpcproto.Empty{}, nil
}

func (s *Server) Status(_ context.Context, in *grpcproto.Empty) (*grpcproto.StatusResponce, error) {
	log.Infof("Status")
	result := api.Status()
	return &grpcproto.StatusResponce{Status: result}, nil
}

func (s *Server) TcpPing(_ context.Context, in *grpcproto.TcpPingRequest) (*grpcproto.TcpPingResponce, error) {
	log.Infof("TcpPing")
	result, err := api.TcpPing(in.GetAddress())
	return &grpcproto.TcpPingResponce{Result: result, Error: fmt.Sprintf("%v", err)}, nil
}

func (s *Server) UrlTest(_ context.Context, in *grpcproto.UrlTestRequest) (*grpcproto.UrlTestResponce, error) {
	log.Infof("UrlTest")
	result, err := api.UrlTest(in.GetUrl(), int(in.GetStandard()))
	return &grpcproto.UrlTestResponce{Result: result, Error: fmt.Sprintf("%v", err)}, nil
}

func (s *Server) CouldStart(_ context.Context, in *grpcproto.Empty) (*grpcproto.CouldStartResponce, error) {
	log.Infof("CouldStart")
	result := api.CouldStart()
	log.Infof("CouldStart: %v", result)
	return &grpcproto.CouldStartResponce{Result: result}, nil
}

func (s *Server) CheckServerAlive(_ context.Context, in *grpcproto.CheckServerAliveRequest) (*grpcproto.CheckServerAliveResponce, error) {
	log.Infof("CheckServerAlive")
	result := api.CheckServerAlive(in.GetAddress(), int(in.GetPort()))
	return &grpcproto.CheckServerAliveResponce{Result: result}, nil
}
