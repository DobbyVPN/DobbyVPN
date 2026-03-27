//go:build android || ios
// +build android ios

package trusttunnel

import (
	"sync"

	"go_client/common"
	log "go_client/logger"
	trusttunnelCommon "go_client/trusttunnel/common"
	"go_client/trusttunnel/internal"
)

type TrustTunnelClient struct {
	manager *internal.TrustTunnelManager
	config  string
	fd      int
	mu      sync.Mutex
}

func NewTrustTunnelClient(config string, fd int) *TrustTunnelClient {
	client := &TrustTunnelClient{
		config: config,
		fd:     fd,
	}
	common.Client.SetVpnClient(trusttunnelCommon.Name, client)
	return client
}

func (c *TrustTunnelClient) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	log.Infof("TrustTunnelClient: Connecting...")

	if c.manager != nil {
		log.Infof("TrustTunnelClient: already connected; skipping Connect()")
		return nil
	}

	c.manager = internal.NewTrustTunnelManager(c.config)

	if err := c.manager.Start(); err != nil {
		log.Infof("TrustTunnelClient: Connection failed: %v", err)
		c.manager.Stop()
		c.manager = nil
		common.Client.MarkInactive(trusttunnelCommon.Name)
		return err
	}

	common.Client.MarkActive(trusttunnelCommon.Name)
	log.Infof("TrustTunnelClient: Connected successfully.")
	return nil
}

func (c *TrustTunnelClient) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	log.Infof("TrustTunnelClient: Disconnecting...")

	if c.manager != nil {
		c.manager.Stop()
		c.manager = nil
	}

	common.Client.MarkInactive(trusttunnelCommon.Name)
	return nil
}

func (c *TrustTunnelClient) Refresh() error {
	_ = c.Disconnect()
	return c.Connect()
}
