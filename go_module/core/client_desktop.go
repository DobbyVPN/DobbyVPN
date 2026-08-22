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

// nativeRuntime is the platform-native resource adapter used by
// sessionapi/runtime. SessionV2 owns the externally meaningful session state
// and generation; this type only tracks the local Connect/Disconnect work
// needed to release native resources safely.
type nativeRuntime struct {
	app        *internal.App
	cancel     context.CancelFunc
	done       chan struct{}
	runErr     error
	state      lifecycleState
	generation uint64

	mu sync.Mutex
}

// desktopShutdownTimeout bounds the wait for platform cleanup after a
// disconnect. Windows removes a generation's owned routes one at a time, so
// the old ten-second bound could expire while cleanup was still progressing.
// The bound remains finite: a genuinely hung native runtime is still reported
// as a cleanup failure.
const desktopShutdownTimeout = 30 * time.Second

func newNativeRuntime(device pkg.ProtocolDevice) *nativeRuntime {
	cfg := common.GetNetworkConfig()

	c := &nativeRuntime{
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
		state: stateIdle,
	}
	return c
}

// NewNativeRuntime exposes only the narrow resource boundary needed by the
// SessionV2 runtime; the concrete lifecycle type remains package-private.
func NewNativeRuntime(device pkg.ProtocolDevice) Runtime { return newNativeRuntime(device) }

func (c *nativeRuntime) Connect() error {
	if c == nil {
		return errors.New("core desktop client is not initialized")
	}

	c.mu.Lock()
	if c.state != stateIdle && c.state != stateFailed {
		state := c.state
		c.mu.Unlock()
		return lifecycleBusyError(state)
	}
	if c.app == nil {
		c.state = stateFailed
		c.mu.Unlock()
		return errors.New("core desktop app is not initialized")
	}
	c.generation++
	generation := c.generation
	c.state = statePreparing
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
				runErr = fmt.Errorf("native session runtime crashed: %v", r)
				log.Debugf(coreCommon.Category, "native session runtime goroutine recovered from panic: %v", runErr)
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
			log.Debugf(coreCommon.Category, "connect native session runtime failed: %v", runErr)
		}
	}()

	// Wait for initialization result with timeout
	select {
	case err := <-initResult:
		if err != nil {
			shutdownErr := c.stopAndWait("after initialization error")
			c.mu.Lock()
			if c.generation == generation {
				c.state = stateFailed
			}
			c.mu.Unlock()
			return errors.Join(fmt.Errorf("failed to initialize native session runtime: %w", err), shutdownErr, c.terminalRunError(generation))
		}
		c.mu.Lock()
		if c.generation != generation || c.state != statePreparing {
			state := c.state
			c.mu.Unlock()
			return fmt.Errorf("native session runtime start generation %d was cancelled while %s", generation, state)
		}
		c.state = stateConnected
		c.mu.Unlock()
		log.Debugf(coreCommon.Category, "Core client connection initialized successfully")
		return nil
	case <-time.After(30 * time.Second):
		shutdownErr := c.stopAndWait("after initialization timeout")
		c.mu.Lock()
		if c.generation == generation {
			c.state = stateFailed
		}
		c.mu.Unlock()
		return errors.Join(fmt.Errorf("timeout waiting for native session runtime initialization"), shutdownErr, c.terminalRunError(generation))
	}
}

func (c *nativeRuntime) Disconnect() error {
	if c == nil {
		return errors.New("core desktop client is not initialized")
	}

	c.mu.Lock()
	if c.state == stateIdle {
		c.mu.Unlock()
		return nil
	}
	if c.state == stateStopping {
		c.mu.Unlock()
		return lifecycleBusyError(stateStopping)
	}
	if c.state == stateFailed && c.done == nil {
		runErr := c.runErr
		c.mu.Unlock()
		if runErr != nil {
			return fmt.Errorf("native session runtime cleanup failed: %w", runErr)
		}
		return nil
	}
	c.state = stateStopping
	cancel := c.cancel
	done := c.done
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if err := c.waitForShutdown(done, "disconnect"); err != nil {
		return err
	}
	if err := c.terminalRunError(c.generationValue()); err != nil {
		return fmt.Errorf("native session runtime cleanup failed: %w", err)
	}
	return nil
}

func (c *nativeRuntime) stopAndWait(reason string) error {
	c.mu.Lock()
	if c.state != stateStopping {
		c.state = stateStopping
	}
	cancel := c.cancel
	done := c.done
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return c.waitForShutdown(done, reason)
}

func (c *nativeRuntime) waitForShutdown(done <-chan struct{}, reason string) error {
	if done == nil {
		return nil
	}
	select {
	case <-done:
		log.Debugf(coreCommon.Category, "Core/app shutdown completed after %s", reason)
		return nil
	case <-time.After(desktopShutdownTimeout):
		log.Debugf(coreCommon.Category, "Core/app shutdown wait timed out after %s", reason)
		return fmt.Errorf("timeout waiting for native session runtime shutdown after %s", reason)
	}
}

func (c *nativeRuntime) finishRun(generation uint64, runErr error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation != generation {
		return
	}
	c.runErr = runErr
	if c.state == stateStopping && runErr == nil {
		c.state = stateIdle
	} else {
		c.state = stateFailed
	}
	c.cancel = nil
	c.done = nil
}

func (c *nativeRuntime) terminalRunError(generation uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation != generation {
		return nil
	}
	return c.runErr
}

func (c *nativeRuntime) stateValue() lifecycleState {
	if c == nil {
		return stateFailed
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

func (c *nativeRuntime) generationValue() uint64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generation
}
