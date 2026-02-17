package main

import (
	"context"
	"fmt"

	pb "go_client/vpnserver"

	log "go_client/logger"
)

type server struct {
	pb.UnimplementedVpnServer
}

func (s *server) StartAwg(_ context.Context, in *pb.StartAwgRequest) (*pb.Empty, error) {
	log.Infof("StartAwg: %v", in.GetTunnel())
	go StartAwg(in.GetTunnel(), in.GetConfig())
	return &pb.Empty{}, nil
}

func (s *server) StopAwg(_ context.Context, in *pb.Empty) (*pb.Empty, error) {
	log.Infof("StopAwg")
	go StopAwg()
	return &pb.Empty{}, nil
}

func (s *server) GetOutlineLastError(_ context.Context, in *pb.Empty) (*pb.GetOutlineLastErrorResponse, error) {
	log.Infof("GetOutlineLastError")
	err := GetOutlineLastError()
	return &pb.GetOutlineLastErrorResponse{Error: err}, nil
}

func (s *server) StartOutline(_ context.Context, in *pb.StartOutlineRequest) (*pb.StartOutlineResponse, error) {
	log.Infof("StartOutline")
	result := StartOutline(in.GetConfig())
	return &pb.StartOutlineResponse{Result: result}, nil
}

func (s *server) StopOutline(_ context.Context, in *pb.Empty) (*pb.Empty, error) {
	log.Infof("StopOutline")
	go StopOutline()
	return &pb.Empty{}, nil
}

func (s *server) StartHealthCheck(_ context.Context, in *pb.StartHealthCheckRequest) (*pb.Empty, error) {
	log.Infof("StartHealthCheck")
	go StartHealthCheck(int(in.GetPeriod()), in.GetSendMetrics())
	return &pb.Empty{}, nil
}

func (s *server) StopHealthCheck(_ context.Context, in *pb.Empty) (*pb.Empty, error) {
	log.Infof("StopHealthCheck")
	go StopHealthCheck()
	return &pb.Empty{}, nil
}

func (s *server) Status(_ context.Context, in *pb.Empty) (*pb.StatusResponce, error) {
	log.Infof("Status")
	result := Status()
	return &pb.StatusResponce{Status: result}, nil
}

func (s *server) TcpPing(_ context.Context, in *pb.TcpPingRequest) (*pb.TcpPingResponce, error) {
	log.Infof("TcpPing")
	result, err := TcpPing(in.GetAddress())
	return &pb.TcpPingResponce{Result: result, Error: fmt.Sprintf("%v", err)}, nil
}

func (s *server) UrlTest(_ context.Context, in *pb.UrlTestRequest) (*pb.UrlTestResponce, error) {
	log.Infof("UrlTest")
	result, err := UrlTest(in.GetUrl(), int(in.GetStandard()))
	return &pb.UrlTestResponce{Result: result, Error: fmt.Sprintf("%v", err)}, nil
}

func (s *server) CouldStart(_ context.Context, in *pb.Empty) (*pb.CouldStartResponce, error) {
	log.Infof("CouldStart")
	result := CouldStart()
	log.Infof("CouldStart: %v", result)
	return &pb.CouldStartResponce{Result: result}, nil
}

func (c *server) CheckServerAlive(_ context.Context, in *pb.CheckServerAliveRequest) (*pb.CheckServerAliveResponce, error) {
	log.Infof("CheckServerAlive")
	result := CheckServerAlive(in.GetAddress(), int(in.GetPort()))
	return &pb.CheckServerAliveResponce{Result: result}, nil
}

func (s *server) StartCloakClient(_ context.Context, in *pb.StartCloakClientRequest) (*pb.Empty, error) {
	log.Infof("StartCloakClient")
	go StartCloakClient(in.GetLocalHost(), in.GetLocalPort(), in.GetConfig(), in.GetUdp())
	return &pb.Empty{}, nil
}

func (s *server) StopCloakClient(_ context.Context, in *pb.Empty) (*pb.Empty, error) {
	log.Infof("StopCloakClient")
	go StopCloakClient()
	return &pb.Empty{}, nil
}

func (c *server) InitLogger(_ context.Context, in *pb.InitLoggerRequest) (*pb.Empty, error) {
	log.Infof("InitLogger")
	go InitLogger(in.Path)
	return &pb.Empty{}, nil
}
