//go:build !windows

package executor

import (
	"flag"
	"fmt"
	"net"

	proto "go_client/desktop_exports/proto"
	protobuf "go_client/vpnserver"

	log "go_client/logger"

	"google.golang.org/grpc"
)

func run(port int) error {
	flag.Parse()
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("failed to listen: %v", err)
	}
	s := grpc.NewServer()

	protobuf.RegisterVpnServer(s, &proto.Server{})

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
