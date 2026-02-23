//go:build android || ios
// +build android ios

package xray

import (
	"sync"

	"go_client/common"
	log "go_client/logger"
	xrayCommon "go_client/xray/common"
	"go_client/xray/internal"
)

type XrayClient struct {
	manager *internal.XrayManager
	config  string
	mu      sync.Mutex
	fd      int
}

func NewXrayClient(vlessConfig string, fileDescriptor int) *XrayClient {
	client := &XrayClient{
		config: vlessConfig,
		fd:     fileDescriptor,
	}
	common.Client.SetVpnClient(xrayCommon.Name, client)
	return client
}

func (c *XrayClient) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	log.Infof("XrayClient: Connecting...")

	if c.manager != nil {
		log.Infof("XrayClient: already connected; skipping Connect()")
		return nil
	}

	c.manager = internal.NewXrayManager(c.config, c.fd)

	if err := c.manager.Start(); err != nil {
		log.Infof("XrayClient: Connection failed: %v", err)
		c.manager.Stop()
		c.manager = nil
		common.Client.MarkInactive(xrayCommon.Name)
		return err
	}

	common.Client.MarkActive(xrayCommon.Name)
	log.Infof("XrayClient: Connected successfully.")
	return nil
}

func (c *XrayClient) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	log.Infof("XrayClient: Disconnecting...")

	if c.manager != nil {
		c.manager.Stop()
		c.manager = nil
	}

	common.Client.MarkInactive(xrayCommon.Name)
	return nil
}

func (c *XrayClient) Refresh() error {
	_ = c.Disconnect()
	return c.Connect()
}
