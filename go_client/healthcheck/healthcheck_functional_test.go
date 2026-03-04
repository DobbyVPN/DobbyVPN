package healthcheck

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestURLTestSuccessfulRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	ms, err := URLTest(server.URL, 1)

	if err != nil {
		t.Fatalf("URLTest failed: %v", err)
	}
	if ms <= 0 {
		t.Errorf("Expected positive latency, got %d ms", ms)
	}
	if ms > 500 {
		t.Errorf("Latency too high for local server: %d ms", ms)
	}
}

func TestURLTestSlowServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ms, err := URLTest(server.URL, 1)

	if err != nil {
		t.Fatalf("URLTest failed: %v", err)
	}
	if ms < 100 {
		t.Errorf("Expected latency >= 100ms for slow server, got %d ms", ms)
	}
}

func TestURLTestServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	ms, err := URLTest(server.URL, 1)

	// Server error should still return latency (connection succeeded)
	if ms <= 0 {
		t.Logf("Server error returned ms=%d, err=%v", ms, err)
	}
}

func TestURLTestInvalidURL(t *testing.T) {
	_, err := URLTest("http://invalid.localhost.test:99999/", 1)

	if err == nil {
		t.Error("Expected error for invalid URL, got nil")
	}
}

func TestURLTestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	_, err := URLTest(server.URL, 1)

	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
}

func TestTCPPingSuccessfulConnection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}
	defer listener.Close()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	ms, err := TCPPing(listener.Addr().String())

	if err != nil {
		t.Fatalf("TCPPing failed: %v", err)
	}
	if ms <= 0 {
		t.Errorf("Expected positive latency, got %d ms", ms)
	}
	if ms > 100 {
		t.Errorf("Latency too high for local connection: %d ms", ms)
	}
}

func TestTCPPingInvalidAddress(t *testing.T) {
	_, err := TCPPing("127.0.0.1:99999")

	if err == nil {
		t.Error("Expected error for closed port, got nil")
	}
}

func TestTCPPingMalformedAddress(t *testing.T) {
	_, err := TCPPing("not-a-valid-address")

	if err == nil {
		t.Error("Expected error for malformed address, got nil")
	}
}

func TestTCPPingEmptyAddress(t *testing.T) {
	_, err := TCPPing("")

	if err == nil {
		t.Error("Expected error for empty address, got nil")
	}
}
