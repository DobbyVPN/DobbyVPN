//go:build !windows

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"

	pb "go_client/vpnserver"

	"google.golang.org/grpc"
)

type executor struct {
}

type server struct {
	pb.UnimplementedVpnServer
}

func (s *server) StartAwg(_ context.Context, in *pb.StartAwgRequest) (*pb.Empty, error) {
	log.Printf("StartAwg: %v", in.GetTunnel())
	go StartAwg(in.GetTunnel(), in.GetConfig())
	return &pb.Empty{}, nil
}

func (s *server) StopAwg(_ context.Context, in *pb.Empty) (*pb.Empty, error) {
	log.Printf("StopAwg")
	go StopAwg()
	return &pb.Empty{}, nil
}

func (s *server) StartOutline(_ context.Context, in *pb.StartOutlineRequest) (*pb.Empty, error) {
	log.Printf("StartOutline")
	go StartOutline(in.GetConfig())
	return &pb.Empty{}, nil
}

func (s *server) StopOutline(_ context.Context, in *pb.Empty) (*pb.Empty, error) {
	log.Printf("StopOutline")
	go StopOutline()
	return &pb.Empty{}, nil
}

func (s *server) StartHealthCheck(_ context.Context, in *pb.StartHealthCheckRequest) (*pb.Empty, error) {
	log.Printf("StartHealthCheck")
	go StartHealthCheck(int(in.GetPeriod()), in.GetSendMetrics())
	return &pb.Empty{}, nil
}

func (s *server) StopHealthCheck(_ context.Context, in *pb.Empty) (*pb.Empty, error) {
	log.Printf("StopHealthCheck")
	go StopHealthCheck()
	return &pb.Empty{}, nil
}

func (s *server) Status(_ context.Context, in *pb.Empty) (*pb.StatusResponce, error) {
	log.Printf("Status")
	result := Status()
	return &pb.StatusResponce{Status: result}, nil
}

func (s *server) TcpPing(_ context.Context, in *pb.TcpPingRequest) (*pb.TcpPingResponce, error) {
	log.Printf("TcpPing")
	result, err := TcpPing(in.GetAddress())
	return &pb.TcpPingResponce{Result: result, Error: fmt.Sprintf("%v", err)}, nil
}

func (s *server) UrlTest(_ context.Context, in *pb.UrlTestRequest) (*pb.UrlTestResponce, error) {
	log.Printf("UrlTest")
	result, err := UrlTest(in.GetUrl(), int(in.GetStandard()))
	return &pb.UrlTestResponce{Result: result, Error: fmt.Sprintf("%v", err)}, nil
}

func (s *server) CouldStart(_ context.Context, in *pb.Empty) (*pb.CouldStartResponce, error) {
	log.Printf("CouldStart")
	result := CouldStart()
	log.Printf("CouldStart:", result)
	return &pb.CouldStartResponce{Result: result}, nil
}

func (s *server) StartCloakClient(_ context.Context, in *pb.StartCloakClientRequest) (*pb.Empty, error) {
	log.Printf("StartCloakClient")
	go StartCloakClient(in.GetLocalHost(), in.GetLocalPort(), in.GetConfig(), in.GetUdp())
	return &pb.Empty{}, nil
}

func (s *server) StopCloakClient(_ context.Context, in *pb.Empty) (*pb.Empty, error) {
	log.Printf("StopCloakClient")
	go StopCloakClient()
	return &pb.Empty{}, nil
}

func run(port int) error {
	flag.Parse()
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("failed to listen: %v", err)
	}
	s := grpc.NewServer()

	pb.RegisterVpnServer(s, &server{})

	log.Printf("server listening at %v", lis.Addr())
	if err := s.Serve(lis); err != nil {
		return fmt.Errorf("failed to serve: %v", err)
	}

	return nil
}

func (c *executor) Execute(port int, mode string) {
	switch mode {
	case "normal":
		run(port)
	default:
		log.Fatalf("Invalid run mode")
	}
}
