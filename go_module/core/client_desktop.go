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
	app        *internal.App
	cancel     context.CancelFunc
	done       chan struct{}
	runErr     error
	state      LifecycleState
	generation uint64

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
		state: StateIdle,
	}
	common.Client.SetVpnClient(coreCommon.Name, c)
	return c
}

func (c *CoreClient) Connect() error {
	if c == nil {
		return errors.New("core desktop client is not initialized")
	}

	c.mu.Lock()
	if c.state != StateIdle && c.state != StateFailed {
		state := c.state
		c.mu.Unlock()
		return lifecycleBusyError(state)
	}
	if c.app == nil {
		c.state = StateFailed
		c.mu.Unlock()
		return errors.New("core desktop app is not initialized")
	}
	c.generation++
	generation := c.generation
	c.state = StatePreparing
	c.runErr = nil

	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.done = make(chan struct{})

	// Channel to receive initialization result from the goroutine
	initResult := make(chan error, 1)
	done := c.done
	c.mu.Unlock()

	go func() {
		var runErr error
		defer func() {
			if r := recover(); r != nil {
				runErr = fmt.Errorf("core client crashed: %v", r)
				log.Debugf(coreCommon.Category, "core goroutine recovered from panic: %v", runErr)
				select {
				case initResult <- runErr:
				default:
				}
			}
			c.finishRun(generation, runErr)
			close(done)
		}()
		runErr = c.app.Run(ctx, initResult)
		if runErr != nil {
			log.Debugf(coreCommon.Category, "connect core client failed: %v", runErr)
		}
	}()

	// Wait for initialization result with timeout
	select {
	case err := <-initResult:
		if err != nil {
			shutdownErr := c.stopAndWait("after initialization error")
			c.mu.Lock()
			if c.generation == generation {
				c.state = StateFailed
			}
			c.mu.Unlock()
			return errors.Join(fmt.Errorf("failed to initialize core client connection: %w", err), shutdownErr, c.terminalRunError(generation))
		}
		c.mu.Lock()
		if c.generation != generation || c.state != StatePreparing {
			state := c.state
			c.mu.Unlock()
			return fmt.Errorf("core client start generation %d was cancelled while %s", generation, state)
		}
		c.state = StateConnected
		c.mu.Unlock()
		log.Debugf(coreCommon.Category, "Core client connection initialized successfully")
		common.Client.MarkActive(coreCommon.Name)
		return nil
	case <-time.After(30 * time.Second):
		shutdownErr := c.stopAndWait("after initialization timeout")
		c.mu.Lock()
		if c.generation == generation {
			c.state = StateFailed
		}
		c.mu.Unlock()
		return errors.Join(fmt.Errorf("timeout waiting for core client connection initialization"), shutdownErr, c.terminalRunError(generation))
	}
}

func (c *CoreClient) Disconnect() error {
	if c == nil {
		return errors.New("core desktop client is not initialized")
	}

	c.mu.Lock()
	if c.state == StateIdle {
		c.mu.Unlock()
		return nil
	}
	if c.state == StateStopping {
		c.mu.Unlock()
		return lifecycleBusyError(StateStopping)
	}
	if c.state == StateFailed && c.done == nil {
		runErr := c.runErr
		c.mu.Unlock()
		if runErr != nil {
			return fmt.Errorf("core client cleanup failed: %w", runErr)
		}
		return nil
	}
	c.state = StateStopping
	cancel := c.cancel
	done := c.done
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if err := c.waitForShutdown(done, "disconnect"); err != nil {
		return err
	}
	if err := c.terminalRunError(c.Generation()); err != nil {
		return fmt.Errorf("core client cleanup failed: %w", err)
	}
	common.Client.MarkInactive(coreCommon.Name)
	return nil
}

func (c *CoreClient) SwitchDevice(device pkg.ProtocolDevice) error {
	if c == nil {
		return errors.New("core desktop client is not initialized")
	}
	if device == nil {
		return errors.New("protocol device is not initialized")
	}

	return fmt.Errorf("protocol changes require a completed disconnect before starting a new session")
}

func (c *CoreClient) stopAndWait(reason string) error {
	c.mu.Lock()
	if c.state != StateStopping {
		c.state = StateStopping
	}
	cancel := c.cancel
	done := c.done
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return c.waitForShutdown(done, reason)
}

func (c *CoreClient) waitForShutdown(done <-chan struct{}, reason string) error {
	if done == nil {
		return nil
	}
	select {
	case <-done:
		log.Debugf(coreCommon.Category, "Core/app shutdown completed after %s", reason)
		return nil
	case <-time.After(10 * time.Second):
		log.Debugf(coreCommon.Category, "Core/app shutdown wait timed out after %s", reason)
		return fmt.Errorf("timeout waiting for core client shutdown after %s", reason)
	}
}

func (c *CoreClient) Refresh() error {
	return fmt.Errorf("core client refresh is unsupported; stop and start a new session")
}

func (c *CoreClient) HealthCheck() error {
	return nil
}

func (c *CoreClient) finishRun(generation uint64, runErr error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation != generation {
		return
	}
	c.runErr = runErr
	if c.state == StateStopping && runErr == nil {
		c.state = StateIdle
	} else {
		c.state = StateFailed
	}
	c.cancel = nil
	c.done = nil
	common.Client.MarkInactive(coreCommon.Name)
}

func (c *CoreClient) terminalRunError(generation uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation != generation {
		return nil
	}
	return c.runErr
}

// State returns the lifecycle state for the active generation.
func (c *CoreClient) State() LifecycleState {
	if c == nil {
		return StateFailed
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Generation increments for every accepted Connect attempt.
func (c *CoreClient) Generation() uint64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generation
}
