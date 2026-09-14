package probe

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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

func TestProbeEndpointReportsFailureStages(t *testing.T) {
	t.Run("connect", func(t *testing.T) {
		result := probeEndpoint(context.Background(), closedLocalHTTPURL(t), time.Second)
		if result.err == nil || result.failureStage != probeStageConnect || result.errorClass != "network" {
			t.Fatalf("result=%+v, want connect/network failure", result)
		}
	})

	t.Run("response timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			time.Sleep(100 * time.Millisecond)
		}))
		defer server.Close()
		result := probeEndpoint(context.Background(), server.URL, 25*time.Millisecond)
		if result.err == nil || result.failureStage != probeStageResponse || result.errorClass != "timeout" {
			t.Fatalf("result=%+v, want response/timeout failure", result)
		}
	})

	t.Run("status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer server.Close()
		result := probeEndpoint(context.Background(), server.URL, time.Second)
		if result.err == nil || result.failureStage != probeStageStatus || result.errorClass != "protocol" {
			t.Fatalf("result=%+v, want status/protocol failure", result)
		}
	})
}

func TestProbeErrorClassUsesOnlyStableCategories(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "deadline", err: context.DeadlineExceeded, want: "timeout"},
		{name: "canceled", err: context.Canceled, want: "canceled"},
		{name: "dns", err: &net.DNSError{Err: "private detail", Name: "private.invalid"}, want: "dns"},
		{name: "protocol", err: http.ErrNotSupported, want: "protocol"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := probeErrorClass(test.err); got != test.want {
				t.Fatalf("probeErrorClass()=%q, want %q", got, test.want)
			}
		})
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
