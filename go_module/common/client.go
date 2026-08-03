package common

import (
	"errors"
	"sync"
)

var (
	ErrClientBusy     = errors.New("VPN client lifecycle operation is still in progress")
	ErrClientNotFound = errors.New("VPN client is not registered")
)

type vpnClientInterface interface {
	Connect() error
	Disconnect() error
	Refresh() error
	HealthCheck() error
}

type vpnClientWithState struct {
	connected     bool
	criticalDepth uint
	vpnClientInterface
}

type CommonClient struct {
	mu         sync.Mutex
	vpnClients map[string]vpnClientWithState
}

func (c *CommonClient) Connect(clientName string) error {
	c.mu.Lock()
	clientState, ok := c.vpnClients[clientName]
	if !ok {
		c.mu.Unlock()
		return ErrClientNotFound
	}
	if clientState.criticalDepth != 0 {
		c.mu.Unlock()
		return ErrClientBusy
	}
	if clientState.connected {
		c.mu.Unlock()
		return nil
	}
	clientState.criticalDepth++
	c.vpnClients[clientName] = clientState
	conn := clientState.vpnClientInterface
	c.mu.Unlock()

	err := conn.Connect()

	c.mu.Lock()
	defer c.mu.Unlock()
	if current, exists := c.vpnClients[clientName]; exists && current.vpnClientInterface == conn {
		if current.criticalDepth > 0 {
			current.criticalDepth--
		}
		if err == nil {
			current.connected = true
		}
		c.vpnClients[clientName] = current
	}
	return err
}

func (c *CommonClient) Disconnect(clientName string) error {
	c.mu.Lock()
	clientState, ok := c.vpnClients[clientName]
	if !ok {
		c.mu.Unlock()
		return ErrClientNotFound
	}
	if clientState.criticalDepth != 0 {
		c.mu.Unlock()
		return ErrClientBusy
	}
	if !clientState.connected {
		c.mu.Unlock()
		return nil
	}
	clientState.criticalDepth++
	c.vpnClients[clientName] = clientState
	conn := clientState.vpnClientInterface
	c.mu.Unlock()

	err := conn.Disconnect()

	c.mu.Lock()
	defer c.mu.Unlock()
	if current, exists := c.vpnClients[clientName]; exists && current.vpnClientInterface == conn {
		if current.criticalDepth > 0 {
			current.criticalDepth--
		}
		if err == nil {
			current.connected = false
		}
		c.vpnClients[clientName] = current
	}
	return err
}

func (c *CommonClient) Refresh(clientName string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if client, ok := c.vpnClients[clientName]; ok && client.connected && client.criticalDepth == 0 {
		return client.Refresh()
	}
	return nil
}

func (c *CommonClient) HealthCheck(clientName string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if client, ok := c.vpnClients[clientName]; ok && client.connected && client.criticalDepth == 0 {
		return client.HealthCheck()
	}
	return nil
}

func (c *CommonClient) SetVpnClient(clientName string, vc vpnClientInterface) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.vpnClients == nil {
		c.vpnClients = make(map[string]vpnClientWithState)
	}
	c.vpnClients[clientName] = vpnClientWithState{vpnClientInterface: vc}
}

func (c *CommonClient) MarkActive(clientName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if client, ok := c.vpnClients[clientName]; ok {
		client.connected = true
		c.vpnClients[clientName] = client
	}
}

func (c *CommonClient) MarkInactive(clientName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if client, ok := c.vpnClients[clientName]; ok {
		client.connected = false
		c.vpnClients[clientName] = client
	}
}

func (c *CommonClient) MarkInCriticalSection(clientName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if client, ok := c.vpnClients[clientName]; ok {
		client.criticalDepth++
		c.vpnClients[clientName] = client
	}
}

func (c *CommonClient) MarkOutOffCriticalSection(clientName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if client, ok := c.vpnClients[clientName]; ok {
		if client.criticalDepth > 0 {
			client.criticalDepth--
		}
		c.vpnClients[clientName] = client
	}
}

func (c *CommonClient) CouldStart() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, client := range c.vpnClients {
		if client.criticalDepth != 0 {
			return false
		}
	}
	return true
}

func (c *CommonClient) GetClientNames(active bool) []string {
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

var Client = &CommonClient{
	vpnClients: make(map[string]vpnClientWithState),
}
