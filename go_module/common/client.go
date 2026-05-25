package common

import (
	"fmt"
	"sync"
)

type vpnClientInterface interface {
	Connect() error
	Disconnect() error
	Refresh() error
}

type vpnClientWithState struct {
	connected         bool
	inCriticalSection bool
	vpnClientInterface
}

type CommonClient struct {
	mu         sync.Mutex
	vpnClients map[string]vpnClientWithState
}

func (c *CommonClient) Connect(clientName string) error {
	if c == nil {
		return fmt.Errorf("failed to connect %s: common client registry is not initialized", clientName)
	}
	c.mu.Lock()
	clientState, ok := c.vpnClients[clientName]
	if !ok || clientState.connected || clientState.inCriticalSection {
		c.mu.Unlock()
		return nil
	}
	clientState.inCriticalSection = true
	c.vpnClients[clientName] = clientState
	conn := clientState.vpnClientInterface
	c.mu.Unlock()

	if conn == nil {
		c.mu.Lock()
		if current, exists := c.vpnClients[clientName]; exists && current.vpnClientInterface == nil {
			current.inCriticalSection = false
			c.vpnClients[clientName] = current
		}
		c.mu.Unlock()
		return fmt.Errorf("failed to connect %s: vpn client is not initialized", clientName)
	}

	err := conn.Connect()

	c.mu.Lock()
	defer c.mu.Unlock()
	if current, exists := c.vpnClients[clientName]; exists && current.vpnClientInterface == conn {
		current.inCriticalSection = false
		if err == nil {
			current.connected = true
		}
		c.vpnClients[clientName] = current
	}
	if err != nil {
		return fmt.Errorf("failed to connect %s: %w", clientName, err)
	}
	return nil
}

func (c *CommonClient) Disconnect(clientName string) error {
	if c == nil {
		return fmt.Errorf("failed to disconnect %s: common client registry is not initialized", clientName)
	}
	c.mu.Lock()
	clientState, ok := c.vpnClients[clientName]
	if !ok || !clientState.connected || clientState.inCriticalSection {
		c.mu.Unlock()
		return nil
	}
	clientState.inCriticalSection = true
	c.vpnClients[clientName] = clientState
	conn := clientState.vpnClientInterface
	c.mu.Unlock()

	if conn == nil {
		c.mu.Lock()
		if current, exists := c.vpnClients[clientName]; exists && current.vpnClientInterface == nil {
			current.inCriticalSection = false
			c.vpnClients[clientName] = current
		}
		c.mu.Unlock()
		return fmt.Errorf("failed to disconnect %s: vpn client is not initialized", clientName)
	}

	err := conn.Disconnect()

	c.mu.Lock()
	defer c.mu.Unlock()
	if current, exists := c.vpnClients[clientName]; exists && current.vpnClientInterface == conn {
		current.inCriticalSection = false
		if err == nil {
			current.connected = false
		}
		c.vpnClients[clientName] = current
	}
	if err != nil {
		return fmt.Errorf("failed to disconnect %s: %w", clientName, err)
	}
	return nil
}

func (c *CommonClient) Refresh(clientName string) error {
	if c == nil {
		return fmt.Errorf("failed to refresh %s: common client registry is not initialized", clientName)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if client, ok := c.vpnClients[clientName]; ok && client.connected && !client.inCriticalSection {
		if client.vpnClientInterface == nil {
			return fmt.Errorf("failed to refresh %s: vpn client is not initialized", clientName)
		}
		if err := client.Refresh(); err != nil {
			return fmt.Errorf("failed to refresh %s: %w", clientName, err)
		}
	}
	return nil
}

func (c *CommonClient) SetVpnClient(clientName string, vc vpnClientInterface) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.vpnClients == nil {
		c.vpnClients = make(map[string]vpnClientWithState)
	}
	c.vpnClients[clientName] = vpnClientWithState{vpnClientInterface: vc}
}

func (c *CommonClient) MarkActive(clientName string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if client, ok := c.vpnClients[clientName]; ok {
		client.connected = true
		c.vpnClients[clientName] = client
	}
}

func (c *CommonClient) MarkInactive(clientName string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if client, ok := c.vpnClients[clientName]; ok {
		client.connected = false
		c.vpnClients[clientName] = client
	}
}

func (c *CommonClient) MarkInCriticalSection(clientName string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if client, ok := c.vpnClients[clientName]; ok {
		client.inCriticalSection = true
		c.vpnClients[clientName] = client
	}
}

func (c *CommonClient) MarkOutOffCriticalSection(clientName string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if client, ok := c.vpnClients[clientName]; ok {
		client.inCriticalSection = false
		c.vpnClients[clientName] = client
	}
}

func (c *CommonClient) CouldStart() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, client := range c.vpnClients {
		if client.inCriticalSection {
			return false
		}
	}
	return true
}

func (c *CommonClient) GetClientNames(active bool) []string {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	names := make([]string, 0, len(c.vpnClients))
	for name, client := range c.vpnClients {
		if client.connected != active {
			continue
		}
		names = append(names, name)
	}
	return names
}

func (c *CommonClient) RefreshAll() error {
	if c == nil {
		return fmt.Errorf("failed to refresh clients: common client registry is not initialized")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for name, client := range c.vpnClients {
		if !client.connected {
			continue
		}
		if client.vpnClientInterface == nil {
			return fmt.Errorf("failed to refresh %s: vpn client is not initialized", name)
		}
		if err := client.Refresh(); err != nil {
			return fmt.Errorf("failed to refresh %s: %w", name, err)
		}
	}
	return nil
}

var Client = &CommonClient{
	vpnClients: make(map[string]vpnClientWithState),
}
