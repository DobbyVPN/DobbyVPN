package proto

import (
	"context"
	"fmt"

	api "go_client/desktop_exports/api"
	log "go_client/logger"
	protobuf "go_client/vpnserver"
)

func (s *Server) StartHealthCheck(_ context.Context, in *protobuf.StartHealthCheckRequest) (*protobuf.Empty, error) {
	log.Infof("StartHealthCheck")
	go api.StartHealthCheck(int(in.GetPeriod()), in.GetSendMetrics())
	return &protobuf.Empty{}, nil
}

func (s *Server) StopHealthCheck(_ context.Context, in *protobuf.Empty) (*protobuf.Empty, error) {
	log.Infof("StopHealthCheck")
	go api.StopHealthCheck()
	return &protobuf.Empty{}, nil
}

func (s *Server) Status(_ context.Context, in *protobuf.Empty) (*protobuf.StatusResponce, error) {
	log.Infof("Status")
	result := api.Status()
	return &protobuf.StatusResponce{Status: result}, nil
}

func (s *Server) TcpPing(_ context.Context, in *protobuf.TcpPingRequest) (*protobuf.TcpPingResponce, error) {
	log.Infof("TcpPing")
	result, err := api.TcpPing(in.GetAddress())
	return &protobuf.TcpPingResponce{Result: result, Error: fmt.Sprintf("%v", err)}, nil
}

func (s *Server) UrlTest(_ context.Context, in *protobuf.UrlTestRequest) (*protobuf.UrlTestResponce, error) {
	log.Infof("UrlTest")
	result, err := api.UrlTest(in.GetUrl(), int(in.GetStandard()))
	return &protobuf.UrlTestResponce{Result: result, Error: fmt.Sprintf("%v", err)}, nil
}

func (s *Server) CouldStart(_ context.Context, in *protobuf.Empty) (*protobuf.CouldStartResponce, error) {
	log.Infof("CouldStart")
	result := api.CouldStart()
	log.Infof("CouldStart: %v", result)
	return &protobuf.CouldStartResponce{Result: result}, nil
}

func (s *Server) CheckServerAlive(_ context.Context, in *protobuf.CheckServerAliveRequest) (*protobuf.CheckServerAliveResponce, error) {
	log.Infof("CheckServerAlive")
	result := api.CheckServerAlive(in.GetAddress(), int(in.GetPort()))
	return &protobuf.CheckServerAliveResponce{Result: result}, nil
}
