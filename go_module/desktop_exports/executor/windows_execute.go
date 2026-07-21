//go:build windows

package executor

import (
	"fmt"
	"net"

	"go_module/core/common"
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

	lis, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", service.serverPort))
	if err != nil {
		log.Debugf(common.Category, "[ERROR] failed to listen: %v", err)
	}
	grpcServer := grpc.NewServer()

	grpcproto.RegisterVpnServer(grpcServer, &proto.Server{})

	go func() {
		log.Debugf(common.Category, "server listening at %v", lis.Addr())
		if err := grpcServer.Serve(lis); err != nil {
			log.Debugf(common.Category, "[ERROR] failed to serve: %v", err)
		}
	}()

loop:
	for c := range r {
		switch c.Cmd {
		case svc.Stop:
			grpcServer.GracefulStop()
			break loop
		default:
			log.Debugf(common.Category, "Unexpected service control request #%d", c)
		}
	}

	changes <- svc.Status{State: svc.StopPending}

	return
}

func runService(port int) error {
	return svc.Run("DobbyVPN vpn service", &managerService{serverPort: port})
}

func run(port int) {
	lis, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		panic(fmt.Sprintf("failed to listen: %v", err))
	}
	s := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			proto.PanicRecoveryUnaryInterceptor(),
			proto.ErrorLoggingUnaryInterceptor(),
		),
	)

	grpcproto.RegisterVpnServer(s, &proto.Server{})

	log.Debugf(common.Category, "server listening at %v", lis.Addr())
	if err := s.Serve(lis); err != nil {
		panic(fmt.Sprintf("failed to serve: %v", err))
	}
}

func (c *Executor) Execute(port int, mode string) {
	log.Debugf(common.Category, "Executing with mode: %v", mode)

	switch mode {
	case "normal":
		run(port)
	case "service":
		runService(port)
	default:
		log.Debugf(common.Category, "[ERROR] Invalid run mode")
	}
}
