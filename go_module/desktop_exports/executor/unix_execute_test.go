//go:build !(windows || android || ios)

package executor

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go_module/desktop_exports/controlplane"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestServeUntilSignalRemovesControlSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime", "control.sock")
	t.Setenv("DOBBYVPN_CONTROL_SOCKET", path)
	listener, err := controlplane.ListenControlSocket()
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	signals := make(chan os.Signal, 1)
	result := make(chan error, 1)
	go func() { result <- serveUntilSignal(server, listener, signals, time.Second) }()

	connection, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()
	signals <- os.Interrupt
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("control server did not stop after signal")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("control socket remains after graceful shutdown: %v", err)
	}
}

func TestServeUntilSignalBoundsActiveRPCShutdown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime", "control.sock")
	t.Setenv("DOBBYVPN_CONTROL_SOCKET", path)
	listener, err := controlplane.ListenControlSocket()
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	healthpb.RegisterHealthServer(server, health.NewServer())
	signals := make(chan os.Signal, 1)
	result := make(chan error, 1)
	go func() { result <- serveUntilSignal(server, listener, signals, 50*time.Millisecond) }()

	clientConnection, err := grpc.NewClient(
		"unix://"+path,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer clientConnection.Close()
	stream, err := healthpb.NewHealthClient(clientConnection).Watch(
		t.Context(), &healthpb.HealthCheckRequest{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	signals <- os.Interrupt
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("active RPC prevented bounded control-server shutdown")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("control socket remains after bounded shutdown: %v", err)
	}
}
