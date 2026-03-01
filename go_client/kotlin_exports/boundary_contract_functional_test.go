//go:build cgo

package main

import (
	"go_client/common"
	"net"
	"strconv"
	"testing"
)

func startProbeListener(t *testing.T) (host string, port int, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on loopback: %v", err)
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
	clearLastError()

	if code := OutlineConnect(); code != -1 {
		t.Fatalf("OutlineConnect should return -1 for nil client, got %d", code)
	}

	if GetLastError() == nil {
		t.Fatal("expected GetLastError to return non-nil after failed connect")
	}

	OutlineDisconnect()
}

func TestHealthcheckBoundaryContractFunctional(t *testing.T) {
	host, port, stop := startProbeListener(t)
	defer stop()

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	if ms, err := TcpPing(addr); err != nil {
		t.Fatalf("TcpPing should succeed for healthy endpoint %s: err=%v", addr, err)
	} else if ms < 0 {
		t.Fatalf("expected non-negative latency for healthy endpoint, got %d", ms)
	}

	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve closed port: %v", err)
	}
	closedPort := closed.Addr().(*net.TCPAddr).Port
	_ = closed.Close()

	closedAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(closedPort))
	if _, err := TcpPing(closedAddr); err == nil {
		t.Fatalf("TcpPing should fail for closed endpoint %s", closedAddr)
	}
}

func TestCloakBoundaryStopIdempotentFunctional(t *testing.T) {
	originalClient := common.Client
	isolatedClient := &common.CommonClient{}
	common.Client = isolatedClient
	t.Cleanup(func() { common.Client = originalClient })

	StartCloakClient(nil, nil, nil, false)

	StopCloakClient()
	StopCloakClient()
}
