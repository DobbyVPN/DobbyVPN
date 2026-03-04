package common

import "sync"

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
	return err
}

func (c *CommonClient) Disconnect(clientName string) error {
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
	return err
}

func (c *CommonClient) Refresh(clientName string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if client, ok := c.vpnClients[clientName]; ok && client.connected && !client.inCriticalSection {
		return client.Refresh()
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
		client.inCriticalSection = true
		c.vpnClients[clientName] = client
	}
}

func (c *CommonClient) MarkOutOffCriticalSection(clientName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if client, ok := c.vpnClients[clientName]; ok {
		client.inCriticalSection = false
		c.vpnClients[clientName] = client
	}
}

func (c *CommonClient) CouldStart() bool {
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
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, client := range c.vpnClients {
		if !client.connected {
			continue
		}
		if err := client.Refresh(); err != nil {
			return err
		}
	}
	return nil
}

var Client = &CommonClient{
	vpnClients: make(map[string]vpnClientWithState),
}
