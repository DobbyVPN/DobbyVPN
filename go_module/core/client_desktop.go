//go:build !(android || ios)

package core

import (
	"context"
	"errors"
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
	done   chan struct{}

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
	if c == nil {
		return errors.New("core desktop client is not initialized")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cancel != nil {
		c.cancel()
		c.waitForShutdownLocked("before reconnect")
		c.cancel = nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.done = make(chan struct{})

	// Channel to receive initialization result from the goroutine
	initResult := make(chan error, 1)
	done := c.done

	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				err := fmt.Errorf("core client crashed: %v", r)
				log.Debugf(coreCommon.Category, "core goroutine recovered from panic: %v", err)
				select {
				case initResult <- err:
				default:
				}
				common.Client.MarkInactive(coreCommon.Name)
			}
		}()
		if c.app == nil {
			err := errors.New("core desktop app is not initialized")
			log.Debugf(coreCommon.Category, "connect core client failed: %v", err)
			common.Client.MarkInactive(coreCommon.Name)
			select {
			case initResult <- err:
			default:
			}
			return
		}
		err := c.app.Run(ctx, initResult)
		if err != nil {
			log.Debugf(coreCommon.Category, "connect core client failed: %v", err)
			common.Client.MarkInactive(coreCommon.Name)
		}
	}()

	// Wait for initialization result with timeout
	select {
	case err := <-initResult:
		if err != nil {
			c.cancel()
			c.waitForShutdownLocked("after initialization error")
			c.cancel = nil
			return fmt.Errorf("failed to initialize core client connection: %w", err)
		}
		log.Debugf(coreCommon.Category, "Core client connection initialized successfully")
		common.Client.MarkActive(coreCommon.Name)
		return nil
	case <-time.After(30 * time.Second):
		c.cancel()
		c.waitForShutdownLocked("after initialization timeout")
		c.cancel = nil
		return fmt.Errorf("timeout waiting for core client connection initialization")
	}
}

func (c *CoreClient) Disconnect() error {
	if c == nil {
		return errors.New("core desktop client is not initialized")
	}

	log.Debugf(coreCommon.Category, "Disconnect: try to lock c.mu")
	c.mu.Lock()
	defer c.mu.Unlock()
	log.Debugf(coreCommon.Category, "Disconnect: locked c.mu")

	if c.cancel != nil {
		log.Debugf(coreCommon.Category, "Disconnect: c.cancel != nil")
		c.cancel()
		c.waitForShutdownLocked("disconnect")
		c.cancel = nil
	}
	log.Debugf(coreCommon.Category, "Disconnect: common.Client.MarkInactive")
	common.Client.MarkInactive(coreCommon.Name)
	log.Debugf(coreCommon.Category, "Disconnect: MarkedInactive")
	return nil
}

func (c *CoreClient) SwitchDevice(device pkg.ProtocolDevice) error {
	if c == nil {
		return errors.New("core desktop client is not initialized")
	}
	if device == nil {
		return errors.New("protocol device is not initialized")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cancel == nil {
		return errors.New("core desktop client is not connected")
	}
	if c.app == nil {
		return errors.New("core desktop app is not initialized")
	}
	return c.app.SwitchProtocolDevice(device)
}

func (c *CoreClient) waitForShutdownLocked(reason string) {
	if c.done == nil {
		return
	}

	done := c.done
	select {
	case <-done:
		log.Debugf(coreCommon.Category, "Core/app shutdown completed after %s", reason)
	case <-time.After(10 * time.Second):
		log.Debugf(coreCommon.Category, "Core/app shutdown wait timed out after %s", reason)
	}
	c.done = nil
}

func (c *CoreClient) Refresh() error {
	if err := c.Disconnect(); err != nil {
		return fmt.Errorf("failed to refresh core client: disconnect failed: %w", err)
	}
	if err := c.Connect(); err != nil {
		return fmt.Errorf("failed to refresh core client: connect failed: %w", err)
	}
	return nil
}

func (c *CoreClient) HealthCheck() error {
	return nil
}
