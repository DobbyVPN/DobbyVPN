package healthcheck

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

func TestTunnelProbeContextCancelsEveryEndpointRequest(t *testing.T) {
	started := make(chan struct{}, 3)
	canceled := make(chan struct{}, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
		canceled <- struct{}{}
	}))
	defer server.Close()

	original := httpProbeURLs
	httpProbeURLs = []string{server.URL, server.URL, server.URL}
	t.Cleanup(func() { httpProbeURLs = original })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int64, 1)
	go func() { done <- MeasureTunnelProbeAverageLatencyMillisWithContext(ctx, 10_000) }()
	for range 3 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("probe did not start every endpoint request")
		}
	}
	cancel()
	select {
	case got := <-done:
		if got != probeFailureResult {
			t.Fatalf("probe result=%d, want failure", got)
		}
	case <-time.After(time.Second):
		t.Fatal("probe did not return after context cancellation")
	}
	for range 3 {
		select {
		case <-canceled:
		case <-time.After(time.Second):
			t.Fatal("endpoint request did not receive cancellation")
		}
	}
}

func TestTunnelProbeContextDeadlineOverridesEndpointTimeout(t *testing.T) {
	started := make(chan struct{}, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()

	original := httpProbeURLs
	httpProbeURLs = []string{server.URL, server.URL, server.URL}
	t.Cleanup(func() { httpProbeURLs = original })

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	startedAt := time.Now()
	if got := MeasureTunnelProbeAverageLatencyMillisWithContext(ctx, 10_000); got != probeFailureResult {
		t.Fatalf("probe result=%d, want failure", got)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("probe ignored context deadline: elapsed=%s", elapsed)
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
