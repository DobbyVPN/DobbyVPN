//go:build !(android || ios)

package outline

import (
	"context"
	log "github.com/sirupsen/logrus"
	"go_client/common"
	"go_client/outline/internal"
	"sync"
)

const Name = "outline"

type OutlineClient struct {
	app    *internal.App
	cancel func()

	mu sync.Mutex
}

func NewClient(transportConfig *string) *OutlineClient {
	c := &OutlineClient{
		app: &internal.App{
			TransportConfig: transportConfig,
			RoutingConfig: &internal.RoutingConfig{
				TunDeviceName:        "outline233",
				TunDeviceIP:          "10.233.233.1",
				TunDeviceMTU:         1500,
				TunGatewayCIDR:       "10.233.233.2/32",
				RoutingTableID:       233,
				RoutingTablePriority: 23333,
				DNSServerIP:          "9.9.9.9",

				BypassCountries: []string{"RU"},
			},
		},
	}
	common.Client.SetVpnClient(Name, c)
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
			log.Errorf("connect outline failed: %v", err)
			common.Client.MarkInactive(Name)
		}
	}()

	common.Client.MarkActive(Name)
	return nil
}

func (c *OutlineClient) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	common.Client.MarkInactive(Name)
	return nil
}

func (c *OutlineClient) Refresh() error {
	_ = c.Disconnect()
	return c.Connect()
}
