package healthcheck

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQuorumHTTPPingCheckSucceedsWhenAllCandidatesWork(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	if err := quorumHTTPPingCheck([]string{server.URL, server.URL, server.URL}); err != nil {
		t.Fatalf("quorumHTTPPingCheck returned error with all candidates working: %v", err)
	}
}

func TestQuorumHTTPPingCheckSucceedsWhenOneCandidateFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	if err := quorumHTTPPingCheck([]string{server.URL, closedLocalHTTPURL(t), server.URL}); err != nil {
		t.Fatalf("quorumHTTPPingCheck returned error with quorum available: %v", err)
	}
}

func TestQuorumHTTPPingCheckFailsWithoutQuorum(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	if err := quorumHTTPPingCheck([]string{closedLocalHTTPURL(t), closedLocalHTTPURL(t), server.URL}); err == nil {
		t.Fatal("quorumHTTPPingCheck returned nil without quorum")
	}
}

func closedLocalHTTPURL(t *testing.T) string {
	t.Helper()

	listenConfig := net.ListenConfig{}
	listener, err := listenConfig.Listen(context.Background(), "tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("Close listener failed: %v", err)
	}
	return "http://" + addr
}
