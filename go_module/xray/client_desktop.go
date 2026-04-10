//go:build !(android || ios)
// +build !android,!ios

package xray

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go_module/common"
	log "go_module/log"
	xrayCommon "go_module/xray/common"
	"go_module/xray/internal"
)

type XrayClient struct {
	app    *internal.App
	cancel func()

	mu sync.Mutex
}

func NewXrayClient(vlessConfig string) *XrayClient {
	c := &XrayClient{
		app: &internal.App{
			VlessConfig: &vlessConfig,
			RoutingConfig: &internal.RoutingConfig{
				TunDeviceName:        "xray233",
				TunDeviceIP:          "10.0.85.2",
				TunDeviceMTU:         1500,
				TunGatewayCIDR:       "10.0.85.1/32",
				RoutingTableID:       233,
				RoutingTablePriority: 23333,
				DNSServerIP:          "9.9.9.9",
			},
		},
	}
	common.Client.SetVpnClient(xrayCommon.Name, c)
	return c
}

func (c *XrayClient) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel

	initResult := make(chan error, 1)

	go func() {
		err := c.app.Run(ctx, initResult)
		if err != nil {
			log.Infof("connect xray failed: %v", err)
			common.Client.MarkInactive(xrayCommon.Name)
		}
	}()

	select {
	case err := <-initResult:
		if err != nil {
			c.cancel()
			c.cancel = nil
			return fmt.Errorf("failed to initialize xray connection: %w", err)
		}
		log.Infof("Xray connection initialized successfully")
		common.Client.MarkActive(xrayCommon.Name)
		return nil
	case <-time.After(30 * time.Second):
		c.cancel()
		c.cancel = nil
		return fmt.Errorf("timeout waiting for xray connection initialization")
	}
}

func (c *XrayClient) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}

	common.Client.MarkInactive(xrayCommon.Name)
	return nil
}

func (c *XrayClient) Refresh() error {
	_ = c.Disconnect()
	return c.Connect()
}
