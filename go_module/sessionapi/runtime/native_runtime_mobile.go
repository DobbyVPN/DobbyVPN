//go:build android || ios

package runtime

import (
	"errors"
	"fmt"
	"go_module/log"
	"go_module/protocol"
	"go_module/tunnel"
	"go_module/tunnel/platform_engine"
	"io"
	"sync"

	"golang.org/x/sys/unix"
)

// nativeRuntime is the platform-native resource adapter used by
// sessionapi/runtime. The session manager owns the externally meaningful
// state and generation; this type only tracks local resource cleanup.
type nativeRuntime struct {
	device protocol.ProtocolDevice
	tun    io.ReadWriteCloser
	engine *tunnel.Engine
	// cleanupErr retains the failed rollback for a repeated Disconnect call.
	cleanupErr        error
	state             lifecycleState
	deviceOpened      bool
	tunOwned          bool
	tunCloseAttempted bool
	tunCloseErr       error
	mu                sync.Mutex
}

func newNativeRuntime(device protocol.ProtocolDevice, tun io.ReadWriteCloser) *nativeRuntime {
	c := &nativeRuntime{
		device: device,
		tun:    tun,
		state:  stateIdle,
	}
	log.Debugf(nativeLogCategory, "mobile session runtime created (tun2socks version)")
	return c
}

func (c *nativeRuntime) Connect() error {
	if c == nil {
		return errors.New("mobile session runtime is not initialized")
	}
	return runLockedWithPanicRecovery("mobile session connect", &c.mu, c.connectLocked, c.disconnectLocked)
}

func (c *nativeRuntime) connectLocked() (err error) {
	if c.state != stateIdle && c.state != stateFailed {
		return lifecycleBusyError(c.state)
	}
	c.state = statePreparing
	c.cleanupErr = nil
	c.deviceOpened = false
	c.tunOwned = false
	c.tunCloseAttempted = false
	c.tunCloseErr = nil
	fail := func(cause error) error {
		c.state = stateFailed
		cleanupErr := c.cleanupResourcesLocked()
		c.cleanupErr = cleanupErr
		c.device, c.tun, c.engine = nil, nil, nil
		return errors.Join(cause, cleanupErr)
	}

	if c.device == nil {
		return fail(errors.New("mobile protocol device is not initialized"))
	}
	if c.tun == nil {
		return fail(errors.New("mobile TUN device is not initialized"))
	}
	c.tunOwned = true

	var fd int
	if f, ok := c.tun.(interface{ Fd() uintptr }); ok {
		fd = int(f.Fd())
		err := unix.SetNonblock(fd, true)
		if err != nil {
			log.Debugf(nativeLogCategory, "Set unix.SetNonblock error: %v", err)
		}
	} else {
		log.Debugf(nativeLogCategory, "failed to get FD from tun: descriptor unavailable")
		return fail(fmt.Errorf("TUN device does not expose a descriptor"))
	}

	err = c.device.Open(0, "")
	if err != nil {
		log.Debugf(nativeLogCategory, "failed to create protocol device: %v", err)
		return fail(fmt.Errorf("failed to open protocol device: %w", err))
	}
	c.deviceOpened = true

	log.Debugf(nativeLogCategory, "starting tun2socks engine proxy_ready=true")
	c.engine, err = tunnel.StartOwnedFDEngine(platform_engine.EngineConfig{
		ProxyAddr:   c.device.GetProxyAddr(),
		FD:          fd,
		UplinkIface: "",
	})
	if err != nil {
		log.Debugf(nativeLogCategory, "Can't start tun2socks: %v", err)
		return fail(fmt.Errorf("failed to start tun2socks engine: %w", err))
	}

	if c.tun != nil {
		if closeErr := c.closeTunLocked(); closeErr != nil {
			log.Debugf(nativeLogCategory, "failed to close local tun fd wrapper after engine start: %v", closeErr)
			return fail(fmt.Errorf("failed to close local TUN after engine start: %w", closeErr))
		}
		c.tunOwned = false
		log.Debugf(nativeLogCategory, "local tun fd wrapper closed after engine start")
		c.tun = nil
	}

	c.state = stateConnected
	log.Debugf(nativeLogCategory, "native session runtime connected successfully via tun2socks")
	return nil
}

func (c *nativeRuntime) closeTunLocked() error {
	if c.tun == nil {
		return nil
	}
	if !c.tunCloseAttempted {
		c.tunCloseAttempted = true
		c.tunCloseErr = c.tun.Close()
	}
	return c.tunCloseErr
}

func (c *nativeRuntime) cleanupResourcesLocked() error {
	var errs []error
	if c.engine != nil {
		engine := c.engine
		c.engine = nil
		errs = append(errs, engine.Stop())
	}
	if c.deviceOpened {
		device := c.device
		c.deviceOpened = false
		if device != nil {
			errs = append(errs, device.Close())
		}
	}
	if c.tunOwned && c.tun != nil {
		tunErr := c.closeTunLocked()
		c.tunOwned = false
		c.tun = nil
		errs = append(errs, tunErr)
	}
	return errors.Join(errs...)
}

func (c *nativeRuntime) Disconnect() error {
	if c == nil {
		return errors.New("mobile session runtime is not initialized")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.disconnectLocked()
}

// disconnectLocked performs cleanup while c.mu is held. Connect's panic
// recovery must use this form because its deferred recovery runs before
// Connect's deferred unlock.
func (c *nativeRuntime) disconnectLocked() error {
	if c.state == stateIdle {
		return nil
	}
	if c.state == stateFailed && c.cleanupErr != nil {
		return fmt.Errorf("native session runtime cleanup failed: %w", c.cleanupErr)
	}
	c.state = stateStopping

	err := c.cleanupResourcesLocked()
	c.cleanupErr = err
	c.engine = nil
	c.tun = nil
	c.device = nil
	if err != nil {
		c.state = stateFailed
	} else {
		c.state = stateIdle
	}

	log.Debugf(nativeLogCategory, "native session runtime disconnected")
	return err
}

func (c *nativeRuntime) stateValue() lifecycleState {
	if c == nil {
		return stateFailed
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}
