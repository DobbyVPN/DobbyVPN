//go:build e2e

package healthcheck

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"go_client/common"
)

type reconnectMockClient struct {
	refreshCalls atomic.Int32
}

func (m *reconnectMockClient) Connect() error { return nil }

func (m *reconnectMockClient) Disconnect() error { return nil }

func (m *reconnectMockClient) Refresh() error {
	m.refreshCalls.Add(1)
	return nil
}

// Verifies unhealthy health check triggers RefreshAll and records successful reconnect state.
func TestHealthCheckTriggersReconnectOnUnhealthyE2E(t *testing.T) {
	StopHealthCheck()
	lastStatus.Store(nil)
	t.Cleanup(func() {
		StopHealthCheck()
		lastStatus.Store(nil)
	})

	originalClient := common.Client
	isolatedClient := &common.CommonClient{}
	common.Client = isolatedClient
	t.Cleanup(func() { common.Client = originalClient })

	mock := &reconnectMockClient{}
	isolatedClient.SetVpnClient("hc-reconnect-e2e", mock)
	isolatedClient.MarkActive("hc-reconnect-e2e")

	originalTransport := httpClient.Transport
	httpClient.Transport = &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("forced unhealthy probe")
		},
	}
	t.Cleanup(func() { httpClient.Transport = originalTransport })

	StartHealthCheck(1, false)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status := lastStatus.Load()
		if status != nil && mock.refreshCalls.Load() > 0 {
			if status.isHealthy {
				t.Fatalf("expected unhealthy status, got healthy")
			}
			if !status.reconnected {
				t.Fatalf("expected reconnect flag to be true")
			}
			if status.reconnectError != nil {
				t.Fatalf("expected nil reconnect error, got %v", status.reconnectError)
			}
			if Status() == "" {
				t.Fatalf("expected non-empty serialized status")
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Fatalf(
		"timed out waiting healthcheck reconnect path; refreshCalls=%d status=%q",
		mock.refreshCalls.Load(),
		Status(),
	)
}
