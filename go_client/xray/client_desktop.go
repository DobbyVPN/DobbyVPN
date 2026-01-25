//go:build !(android || ios)
// +build !android,!ios

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
}

func NewXrayClient(vlessConfig string) *XrayClient {
	client := &XrayClient{
		config: vlessConfig,
	}
	common.Client.SetVpnClient(xrayCommon.Name, client)
	return client
}

func (c *XrayClient) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	log.Infof("XrayClient: Connecting...")

	c.manager = internal.NewXrayManager(c.config)

	if err := c.manager.Start(); err != nil {
		log.Infof("XrayClient: Connection failed: %v", err)
		c.manager.Stop()
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
