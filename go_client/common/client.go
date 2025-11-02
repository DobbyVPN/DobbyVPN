package common

import "sync"
import log "github.com/sirupsen/logrus"

type vpnClient interface {
	Connect() error
	Disconnect() error
	Refresh() error
}

type ClientState int

const (
	Disconnected ClientState = iota
	InProcess
	Connected
)

type vpnClientWithState struct {
	state ClientState
	vpnClient
}

type CommonClient struct {
	mu         sync.Mutex
	vpnClients map[string]vpnClientWithState
}

func (c *CommonClient) Connect(clientName string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if client, ok := c.vpnClients[clientName]; ok && client.state == Disconnected {
		err := client.Connect()
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *CommonClient) Disconnect(clientName string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if client, ok := c.vpnClients[clientName]; ok && client.state == Connected {
		c.mu.Unlock()
		err := client.Disconnect()
		c.mu.Lock()
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *CommonClient) SetVpnClient(clientName string, client vpnClient) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.vpnClients == nil {
		c.vpnClients = make(map[string]vpnClientWithState)
	}
	c.vpnClients[clientName] = vpnClientWithState{vpnClient: client}
}

func (c *CommonClient) MarkActive(clientName string) {
	log.Infof("Start MarkActive %v\n", clientName)
	c.mu.Lock()
	defer c.mu.Unlock()
	if client, ok := c.vpnClients[clientName]; ok {
		client.state = Connected
		c.vpnClients[clientName] = client
		log.Infof("Marked Active %v\n", clientName)
	}
}

func (c *CommonClient) MarkInactive(clientName string) {
	log.Infof("Start MarkInactive %v\n", clientName)
	c.mu.Lock()
	defer c.mu.Unlock()
	if client, ok := c.vpnClients[clientName]; ok {
		client.state = Disconnected
		c.vpnClients[clientName] = client
		log.Infof("Marked Inactive %v\n", clientName)
	}
}

func (c *CommonClient) MarkInProgress(clientName string) {
	log.Infof("Start MarkInProgress %v\n", clientName)
	c.mu.Lock()
	defer c.mu.Unlock()
	if client, ok := c.vpnClients[clientName]; ok {
		client.state = InProcess
		c.vpnClients[clientName] = client
		log.Infof("Marked InProgress %v\n", clientName)
	}
}

func (c *CommonClient) CouldStart() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for name, client := range c.vpnClients {
		log.Infof("Call CouldStart: name = %v, state = %v\n", name, client.state)
		if client.state != Connected && client.state != Disconnected {
			return false
		}
	}
	return true
}

func (c *CommonClient) RefreshAll() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, client := range c.vpnClients {
		if client.state != Connected {
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
