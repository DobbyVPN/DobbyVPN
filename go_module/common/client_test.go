package common

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type lifecycleClient struct {
	connectCalls    atomic.Int32
	disconnectCalls atomic.Int32
	disconnectGate  <-chan struct{}
}

func (c *lifecycleClient) Connect() error {
	c.connectCalls.Add(1)
	return nil
}

func (c *lifecycleClient) Disconnect() error {
	c.disconnectCalls.Add(1)
	if c.disconnectGate != nil {
		<-c.disconnectGate
	}
	return nil
}

func (*lifecycleClient) Refresh() error     { return nil }
func (*lifecycleClient) HealthCheck() error { return nil }

func newTestCommonClient(name string, client vpnClientInterface) *CommonClient {
	return &CommonClient{vpnClients: map[string]vpnClientWithState{
		name: {vpnClientInterface: client},
	}}
}

func TestConnectReturnsBusyInsteadOfSilentSuccess(t *testing.T) {
	const name = "test"
	client := &lifecycleClient{}
	coordinator := newTestCommonClient(name, client)

	coordinator.MarkInCriticalSection(name)
	if err := coordinator.Connect(name); !errors.Is(err, ErrClientBusy) {
		t.Fatalf("Connect() error = %v, want ErrClientBusy", err)
	}
	if got := client.connectCalls.Load(); got != 0 {
		t.Fatalf("underlying Connect calls = %d, want 0", got)
	}
}

func TestNestedCriticalSectionCannotBeClearedPrematurely(t *testing.T) {
	const name = "test"
	client := &lifecycleClient{}
	coordinator := newTestCommonClient(name, client)

	coordinator.MarkInCriticalSection(name)
	coordinator.MarkInCriticalSection(name)
	coordinator.MarkOutOffCriticalSection(name)
	if coordinator.CouldStart() {
		t.Fatal("CouldStart() = true while an outer critical section remains")
	}
	if err := coordinator.Connect(name); !errors.Is(err, ErrClientBusy) {
		t.Fatalf("Connect() error = %v, want ErrClientBusy", err)
	}

	coordinator.MarkOutOffCriticalSection(name)
	if err := coordinator.Connect(name); err != nil {
		t.Fatalf("Connect() after balanced release error = %v", err)
	}
	if got := client.connectCalls.Load(); got != 1 {
		t.Fatalf("underlying Connect calls = %d, want 1", got)
	}
}

func TestConnectCannotOverlapDisconnectCleanup(t *testing.T) {
	const name = "test"
	cleanup := make(chan struct{})
	client := &lifecycleClient{disconnectGate: cleanup}
	coordinator := newTestCommonClient(name, client)
	if err := coordinator.Connect(name); err != nil {
		t.Fatal(err)
	}

	disconnected := make(chan error, 1)
	go func() { disconnected <- coordinator.Disconnect(name) }()
	deadline := time.After(time.Second)
	for client.disconnectCalls.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("underlying Disconnect was not called")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := coordinator.Connect(name); !errors.Is(err, ErrClientBusy) {
		t.Fatalf("Connect() during cleanup error = %v, want ErrClientBusy", err)
	}

	close(cleanup)
	if err := <-disconnected; err != nil {
		t.Fatalf("Disconnect() error = %v", err)
	}
	if err := coordinator.Connect(name); err != nil {
		t.Fatalf("Connect() after cleanup error = %v", err)
	}
}
