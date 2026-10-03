//go:build !(android || ios)

package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"core/common"
	"core/dnscache"
	"core/log"
	"core/protocol"
	"core/sessionapi"
	"core/sessionapi/runtime/internal"
	"core/tunnel"
)

// nativeRuntime is the platform-native resource adapter used by
// sessionapi/runtime. The session manager owns externally meaningful state
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

func newNativeRuntime(device protocol.ProtocolDevice, dnsCache *dnscache.Cache, bypass *tunnel.BypassPolicy) *nativeRuntime {
	cfg := common.GetNetworkConfig()

	c := &nativeRuntime{
		app: &internal.App{
			ProtocolDevice: device,
			DNSCache:       dnsCache,
			BypassPolicy:   bypass,
			RoutingConfig: &internal.RoutingConfig{
				TunDeviceName:        ownedTunName,
				TunDeviceIP:          cfg.TunDevice,
				TunDeviceMTU:         1500,
				TunGatewayCIDR:       cfg.TunGateway + "/32",
				RoutingTableID:       ownedRoutingTableID,
				RoutingTablePriority: ownedRoutingPriority,
				DNSServerIP:          "9.9.9.9",
			},
		},
		state: stateIdle,
	}
	return c
}

func (c *nativeRuntime) Connect(ctx context.Context) error {
	if c == nil {
		return errors.New("desktop session runtime is not initialized")
	}

	c.mu.Lock()
	if c.state != stateIdle {
		state := c.state
		c.mu.Unlock()
		return lifecycleBusyError(state)
	}
	if c.app == nil {
		c.state = stateFailed
		c.mu.Unlock()
		return errors.New("desktop session runtime app is not initialized")
	}
	c.generation++
	generation := c.generation
	c.state = statePreparing
	c.runErr = nil

	ctx, cancel := context.WithCancel(ctx)
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
				log.Debugf(nativeLogCategory, "native session runtime goroutine recovered from panic: %v", runErr)
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
			log.Debugf(nativeLogCategory, "connect native session runtime failed: %v", runErr)
		}
	}()

	// Wait for initialization result with timeout
	select {
	case err := <-initResult:
		if err != nil {
			shutdownErr := c.stopAndWait(sessionapi.CleanupContext(ctx), "after initialization error")
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
		log.Debugf(nativeLogCategory, "Desktop session runtime initialized successfully")
		return nil
	case <-ctx.Done():
		return errors.Join(ctx.Err(), c.stopAndWait(sessionapi.CleanupContext(ctx), "after acquisition cancellation"))
	case <-time.After(30 * time.Second):
		shutdownErr := c.stopAndWait(sessionapi.CleanupContext(ctx), "after initialization timeout")
		c.mu.Lock()
		if c.generation == generation {
			c.state = stateFailed
		}
		c.mu.Unlock()
		return errors.Join(fmt.Errorf("timeout waiting for native session runtime initialization"), shutdownErr, c.terminalRunError(generation))
	}
}

func (c *nativeRuntime) Disconnect(ctx context.Context) error {
	if c == nil {
		return errors.New("desktop session runtime is not initialized")
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
		if c.app != nil {
			err := c.app.Close(ctx)
			if err == nil {
				c.state = stateIdle
			}
			c.mu.Unlock()
			return err
		}
		runErr := c.runErr
		c.mu.Unlock()
		return runErr
	}

	c.state = stateStopping
	cancel := c.cancel
	done := c.done
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if err := c.waitForShutdown(ctx, done, "disconnect"); err != nil {
		return err
	}
	if c.app != nil {
		if err := c.app.CleanupError(); err != nil {
			return &sessionapi.CleanupFailure{Err: err}
		}
		c.mu.Lock()
		c.state = stateIdle
		c.mu.Unlock()
	} else if err := c.terminalRunError(c.generationValue()); err != nil {
		return fmt.Errorf("native session runtime cleanup failed: %w", err)
	}

	return nil
}

func (c *nativeRuntime) stopAndWait(ctx context.Context, reason string) error {
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
	return c.waitForShutdown(ctx, done, reason)
}

func (c *nativeRuntime) waitForShutdown(ctx context.Context, done <-chan struct{}, reason string) error {
	if done == nil {
		return nil
	}
	select {
	case <-done:
		log.Debugf(nativeLogCategory, "Desktop session runtime shutdown completed after %s", reason)
		return nil
	case <-ctx.Done():
		log.Debugf(nativeLogCategory, "Desktop session runtime shutdown wait timed out after %s", reason)
		return fmt.Errorf("waiting for native session runtime shutdown after %s: %w", reason, ctx.Err())
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
