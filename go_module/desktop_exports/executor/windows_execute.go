//go:build windows

package executor

import (
	"fmt"
	"net"

	"go_module/desktop_exports/proto"
	"go_module/grpcproto"

	"go_module/log"

	"golang.org/x/sys/windows/svc"
	"google.golang.org/grpc"
)

type managerService struct {
	serverPort int
}

func (service *managerService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (svcSpecificEC bool, exitCode uint32) {
	changes <- svc.Status{State: svc.StartPending}
	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptSessionChange}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", service.serverPort))
	if err != nil {
		log.Infof("[ERROR] failed to listen: %v", err)
	}
	grpcServer := grpc.NewServer()

	grpcproto.RegisterVpnServer(grpcServer, &proto.Server{})

	go func() {
		log.Infof("server listening at %v", lis.Addr())
		if err := grpcServer.Serve(lis); err != nil {
			log.Infof("[ERROR] failed to serve: %v", err)
		}
	}()

loop:
	for c := range r {
		switch c.Cmd {
		case svc.Stop:
			grpcServer.GracefulStop()
			break loop
		default:
			log.Infof("Unexpected service control request #%d", c)
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

	grpcproto.RegisterVpnServer(s, &proto.Server{})

	log.Infof("server listening at %v", lis.Addr())
	if err := s.Serve(lis); err != nil {
		return fmt.Errorf("failed to serve: %v", err)
	}

	return nil
}

func (c *Executor) Execute(port int, mode string) {
	log.Infof("Executing with mode: %v", mode)

	switch mode {
	case "normal":
		run(port)
	case "service":
		runService(port)
	default:
		log.Infof("[ERROR] Invalid run mode")
	}
}
