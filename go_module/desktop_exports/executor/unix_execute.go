//go:build !(windows || android || ios)

package executor

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go_module/core/common"
	"go_module/desktop_exports/controlplane"
	"go_module/desktop_exports/proto"
	"go_module/grpcproto"

	"go_module/log"

	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

const gracefulStopTimeout = 5 * time.Second

func serveUntilSignal(server *grpc.Server, listener net.Listener, signals <-chan os.Signal, gracePeriod time.Duration) error {
	defer listener.Close()
	completed := make(chan struct{})
	go func() {
		select {
		case <-signals:
		case <-completed:
			return
		}
		graceful := make(chan struct{})
		go func() {
			server.GracefulStop()
			close(graceful)
		}()
		timer := time.NewTimer(gracePeriod)
		defer timer.Stop()
		select {
		case <-graceful:
		case <-timer.C:
			server.Stop()
		case <-signals:
			server.Stop()
		case <-completed:
		}
	}()
	err := server.Serve(listener)
	close(completed)
	if errors.Is(err, grpc.ErrServerStopped) {
		return nil
	}
	return err
}

func run(_ int) {
	// Convert logrus.Fatal (os.Exit) into a panic so goroutines can recover from it
	// instead of crashing the entire gRPC server process.
	logrus.StandardLogger().ExitFunc = func(code int) {
		panic(fmt.Sprintf("fatal error (exit code %d)", code))
	}

	flag.Parse()
	lis, err := controlplane.ListenControlSocket()
	if err != nil {
		panic(fmt.Sprintf("failed to listen: %v", err))
	}
	s := grpc.NewServer(
		grpc.Creds(controlplane.UnixPeerCredentials{}),
		grpc.ChainUnaryInterceptor(
			proto.ControlAuthUnaryInterceptor(false, ""),
			proto.PanicRecoveryUnaryInterceptor(),
			proto.ErrorLoggingUnaryInterceptor(),
		),
	)

	grpcproto.RegisterVpnServer(s, &proto.Server{})

	log.Debugf(common.Category, "desktop control socket ready")
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	if err := serveUntilSignal(s, lis, signals, gracefulStopTimeout); err != nil {
		panic(fmt.Sprintf("failed to serve: %v", err))
	}
}

func (c *Executor) Execute(port int, mode string) {
	switch mode {
	case "normal":
		run(port)
	default:
		log.Debugf(common.Category, "[ERROR] Invalid run mode")
	}
}
