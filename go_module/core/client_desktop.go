//go:build !(android || ios)

package core

import (
	"context"
	"fmt"
	"go_module/common"
	coreCommon "go_module/core/common"
	"go_module/core/internal"
	"go_module/core/pkg"
	"go_module/log"
	"sync"
	"time"
)

type CoreClient struct {
	app    *internal.App
	cancel func()

	mu sync.Mutex
}

func NewClient(device pkg.ProtocolDevice) *CoreClient {
	cfg := common.GetNetworkConfig()

	c := &CoreClient{
		app: &internal.App{
			ProtocolDevice: device,
			RoutingConfig: &internal.RoutingConfig{
				TunDeviceName:        "dobby233",
				TunDeviceIP:          cfg.TunDevice,
				TunDeviceMTU:         1500,
				TunGatewayCIDR:       cfg.TunGateway + "/32",
				RoutingTableID:       233,
				RoutingTablePriority: 23333,
				DNSServerIP:          "9.9.9.9",
			},
		},
	}
	common.Client.SetVpnClient(coreCommon.Name, c)
	return c
}

func (c *CoreClient) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel

	// Channel to receive initialization result from the goroutine
	initResult := make(chan error, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				err := fmt.Errorf("core client crashed: %v", r)
				log.Infof("core goroutine recovered from panic: %v", err)
				select {
				case initResult <- err:
				default:
				}
				common.Client.MarkInactive(coreCommon.Name)
			}
		}()
		err := c.app.Run(ctx, initResult)
		if err != nil {
			log.Infof("connect core client failed: %v", err)
			common.Client.MarkInactive(coreCommon.Name)
		}
	}()

	// Wait for initialization result with timeout
	select {
	case err := <-initResult:
		if err != nil {
			c.cancel()
			c.cancel = nil
			return fmt.Errorf("failed to initialize core client connection: %w", err)
		}
		log.Infof("Core client connection initialized successfully")
		common.Client.MarkActive(coreCommon.Name)
		return nil
	case <-time.After(30 * time.Second):
		c.cancel()
		c.cancel = nil
		return fmt.Errorf("timeout waiting for core client connection initialization")
	}
}

func (c *CoreClient) Disconnect() error {
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
	common.Client.MarkInactive(coreCommon.Name)
	log.Infof("Disconnect: MarkedInactive")
	return nil
}

func (c *CoreClient) Refresh() error {
	_ = c.Disconnect()
	return c.Connect()
}
