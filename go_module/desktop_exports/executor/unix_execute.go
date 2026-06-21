//go:build !(windows || android || ios)

package executor

import (
	"flag"
	"fmt"
	"net"

	"go_module/core/common"
	"go_module/desktop_exports/proto"
	"go_module/grpcproto"

	"go_module/log"

	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

func run(port int) error {
	// Convert logrus.Fatal (os.Exit) into a panic so goroutines can recover from it
	// instead of crashing the entire gRPC server process.
	logrus.StandardLogger().ExitFunc = func(code int) {
		panic(fmt.Sprintf("fatal error (exit code %d)", code))
	}

	flag.Parse()
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("failed to listen: %v", err)
	}
	s := grpc.NewServer()

	grpcproto.RegisterVpnServer(s, &proto.Server{})

	log.Debugf(common.Category, "server listening at %v", lis.Addr())
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
		log.Debugf(common.Category, "[ERROR] Invalid run mode")
	}
}
