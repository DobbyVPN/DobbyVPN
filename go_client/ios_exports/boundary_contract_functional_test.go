//go:build darwin

package cloak_outline

import (
	"go_client/common"
	"net"
	"strings"
	"testing"
)

func startHealthProbeListener(t *testing.T) (host string, port int, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start tcp listener: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port, func() {
		_ = ln.Close()
		<-done
	}
}

func TestOutlineBoundaryNilClientFunctional(t *testing.T) {
	client = nil

	if err := OutlineDisconnect(); err != nil {
		t.Fatalf("OutlineDisconnect should be idempotent for nil client: %v", err)
	}

	err := OutlineConnect()
	if err == nil {
		t.Fatal("expected OutlineConnect to fail when client is nil")
	}
	if !strings.Contains(err.Error(), "client is nil") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHealthcheckBoundaryContractFunctional(t *testing.T) {
	host, port, stop := startHealthProbeListener(t)
	defer stop()

	if code := CheckServerAlive(host, port); code != 0 {
		t.Fatalf("CheckServerAlive should return 0 for healthy endpoint, got %d", code)
	}

	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve closed port: %v", err)
	}
	closedPort := closed.Addr().(*net.TCPAddr).Port
	_ = closed.Close()

	if code := CheckServerAlive("127.0.0.1", closedPort); code != -1 {
		t.Fatalf("CheckServerAlive should return -1 for closed endpoint, got %d", code)
	}
}

func TestCloakBoundaryStopIdempotentFunctional(t *testing.T) {
	originalClient := common.Client
	isolatedClient := &common.CommonClient{}
	common.Client = isolatedClient
	t.Cleanup(func() { common.Client = originalClient })

	StartCloakClient("127.0.0.1", "0", `{"UID":"bad","PublicKey":"bad","RemoteHost":"invalid.invalid"}`, false)
	StopCloakClient()
	StopCloakClient()
}
