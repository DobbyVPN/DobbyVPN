//go:build !windows

package executor

import (
	"flag"
	"fmt"
	"net"

	"go_module/desktop_exports/proto"
	"go_module/grpcproto"

	"go_module/log"

	"google.golang.org/grpc"
)

func run(port int) error {
	flag.Parse()
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
	switch mode {
	case "normal":
		run(port)
	default:
		log.Infof("[ERROR] Invalid run mode")
	}
}
