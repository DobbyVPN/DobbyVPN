//go:build windows

package main

import (
	"fmt"
	"log"
	"net"

	pb "go_client/vpnserver"

	"golang.org/x/sys/windows/svc"
	"google.golang.org/grpc"
)

type executor struct {
}

type managerService struct {
	serverPort int
}

func (service *managerService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (svcSpecificEC bool, exitCode uint32) {
	changes <- svc.Status{State: svc.StartPending}
	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptSessionChange}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", service.serverPort))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	grpcServer := grpc.NewServer()

	pb.RegisterVpnServer(grpcServer, &server{})

	go func() {
		log.Printf("server listening at %v", lis.Addr())
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
	}()

loop:
	for c := range r {
		switch c.Cmd {
		case svc.Stop:
			grpcServer.GracefulStop()
			break loop
		default:
			log.Printf("Unexpected service control request #%d", c)
		}
	}

	changes <- svc.Status{State: svc.StopPending}

	return
}

func runService(port int) error {
	return svc.Run("DobbyVPN vpn service", &managerService{serverPort: port})
}

func run(port int) error {
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
	log.Printf("Executing with mode: %v\n", mode)

	switch mode {
	case "normal":
		run(port)
	case "service":
		runService(port)
	default:
		log.Fatalf("Invalid run mode")
	}
}
