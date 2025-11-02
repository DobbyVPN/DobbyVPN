//go:build !(android || ios)

package outline

import (
	"context"
	log "github.com/sirupsen/logrus"
	"go_client/common"
	outlineCommon "go_client/outline/common"
	"go_client/outline/internal"
	"sync"
)

type OutlineClient struct {
	app    *internal.App
	cancel func()

	mu sync.Mutex
}

func NewClient(transportConfig string) *OutlineClient {
	c := &OutlineClient{
		app: &internal.App{
			TransportConfig: &transportConfig,
			RoutingConfig: &internal.RoutingConfig{
				TunDeviceName:        "outline233",
				TunDeviceIP:          "10.0.85.2",
				TunDeviceMTU:         1500,
				TunGatewayCIDR:       "10.0.85.1/32",
				RoutingTableID:       233,
				RoutingTablePriority: 23333,
				DNSServerIP:          "9.9.9.9",
			},
		},
	}
	common.Client.SetVpnClient(outlineCommon.Name, c)
	return c
}

func (c *OutlineClient) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel

	go func() {
		if err := c.app.Run(ctx); err != nil {
			log.Infof("connect outline failed: %v", err)
		}
	}()
	return nil
}

func (c *OutlineClient) Disconnect() error {
	log.Infof("Disconnect: try to lock c.mu")
	c.mu.Lock()
	defer c.mu.Unlock()
	log.Infof("Disconnect: locked c.mu")

	if c.cancel != nil {
		log.Infof("Disconnect: c.cancel != nil")
		c.cancel()
		c.cancel = nil
	}
	log.Infof("Disconnect: common.Client.MarkInactive")
	common.Client.MarkInactive(outlineCommon.Name)
	log.Infof("Disconnect: MarkedInactive")
	return nil
}

func (c *OutlineClient) Refresh() error {
	_ = c.Disconnect()
	return c.Connect()
}
