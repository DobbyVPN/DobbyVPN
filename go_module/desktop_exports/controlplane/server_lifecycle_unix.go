//go:build !(windows || android || ios)

package controlplane

import (
	"errors"
	"net"
	"os"
	"time"

	"google.golang.org/grpc"
)

const defaultGracefulStopTimeout = 5 * time.Second

// ServeUntilSignal runs the desktop control server until shutdown is requested.
// It bounds graceful shutdown so an active RPC cannot leave the privileged
// service or its control socket behind indefinitely.
func ServeUntilSignal(server *grpc.Server, listener net.Listener, signals <-chan os.Signal) error {
	return serveUntilSignal(server, listener, signals, defaultGracefulStopTimeout)
}

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
