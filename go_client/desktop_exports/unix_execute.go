//go:build !windows

package main

import (
	"flag"
	"fmt"
	"net"

	pb "go_client/vpnserver"

	log "go_client/logger"

	"google.golang.org/grpc"
)

type executor struct {
}

func run(port int) error {
	flag.Parse()
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("failed to listen: %v", err)
	}
	s := grpc.NewServer()

	pb.RegisterVpnServer(s, &server{})

	log.Infof("server listening at %v", lis.Addr())
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
		log.Infof("[ERROR] Invalid run mode")
	}
}
