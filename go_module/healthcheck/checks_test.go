package healthcheck

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAllHTTPPingCheckSucceedsWhenAllCandidatesWork(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	if err := allHTTPPingCheck([]string{server.URL, server.URL, server.URL}); err != nil {
		t.Fatalf("allHTTPPingCheck returned error with all candidates working: %v", err)
	}
}

func TestAllHTTPPingCheckFailsWhenAnyCandidateFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	if err := allHTTPPingCheck([]string{server.URL, closedLocalHTTPURL(t), server.URL}); err == nil {
		t.Fatal("allHTTPPingCheck returned nil when one candidate failed")
	}
}

func closedLocalHTTPURL(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("Close listener failed: %v", err)
	}
	return "http://" + addr
}
