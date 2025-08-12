package common

import (
	"fmt"
	"testing"
)

type mockVpnClient struct {
	connected bool
	refreshed bool
}

func (c *mockVpnClient) Connect() error {
	if c.connected {
		return fmt.Errorf("already connected")
	}
	c.connected = true
	return nil
}

func (c *mockVpnClient) Disconnect() error {
	if !c.connected {
		return fmt.Errorf("not connected")
	}
	c.connected = false
	return nil
}

func (c *mockVpnClient) Refresh() error {
	c.refreshed = true
	return nil
}

func TestCommonClient(t *testing.T) {
	client := &CommonClient{
		vpnClients: make(map[string]*vpnClientWithState),
	}

	mockClient1 := &mockVpnClient{}
	mockClient2 := &mockVpnClient{}

	client.SetVpnClient("mock1", mockClient1)
	client.SetVpnClient("mock2", mockClient2)

	names := client.GetClientNames(false)
	if len(names) != 2 {
		t.Errorf("expected 2 clients, got %d", len(names))
	}

	client.MarkActive("mock1")
	names = client.GetClientNames(true)
	if len(names) != 1 || names[0] != "mock1" {
		t.Errorf("expected 1 active client, got %v", names)
	}

	client.Connect("mock2")
	names = client.GetClientNames(true)
	if len(names) != 2 {
		t.Errorf("expected 2 active clients, got %d", len(names))
	}

	if !mockClient2.connected {
		t.Errorf("expected mockClient2 to be connected")
	}
}
