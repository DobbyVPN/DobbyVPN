package common

import "sync"

type vpnClient interface {
	Connect() error
	Disconnect() error
	Refresh() error
}

type vpnClientWithState struct {
	connected bool
	vpnClient
}

type CommonClient struct {
	mu         sync.Mutex
	vpnClients map[string]*vpnClientWithState
}

func (c *CommonClient) Connect(clientName string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if client, ok := c.vpnClients[clientName]; ok && !client.connected {
		err := client.Connect()
		if err != nil {
			return err
		}
		client.connected = true
	}
	return nil
}

func (c *CommonClient) Disconnect(clientName string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if client, ok := c.vpnClients[clientName]; ok && client.connected {
		err := client.Disconnect()
		if err != nil {
			return err
		}
		client.connected = false
	}
	return nil
}

func (c *CommonClient) Refresh(clientName string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if client, ok := c.vpnClients[clientName]; ok && client.connected {
		return client.Refresh()
	}
	return nil
}

func (c *CommonClient) SetVpnClient(clientName string, client vpnClient) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.vpnClients == nil {
		c.vpnClients = make(map[string]*vpnClientWithState)
	}
	c.vpnClients[clientName] = &vpnClientWithState{vpnClient: client}
}

func (c *CommonClient) MarkActive(clientName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if client, ok := c.vpnClients[clientName]; ok {
		client.connected = true
	}
}

func (c *CommonClient) MarkInactive(clientName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if client, ok := c.vpnClients[clientName]; ok {
		client.connected = false
	}
}

func (c *CommonClient) GetClientNames(active bool) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var names []string
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
	vpnClients: make(map[string]*vpnClientWithState),
}
