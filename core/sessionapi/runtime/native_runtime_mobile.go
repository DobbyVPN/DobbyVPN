//go:build android || ios

package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"core/dnscache"
	"core/log"
	"core/protocol"
	"core/sessionapi"
	"core/tunnel"
	"core/tunnel/platform_engine"

	"golang.org/x/sys/unix"
)

// nativeRuntime is the platform-native resource adapter used by
// sessionapi/runtime. The session manager owns the externally meaningful
// state and generation; this type only tracks local resource cleanup.
type nativeRuntime struct {
	device       protocol.ProtocolDevice
	tun          io.ReadWriteCloser
	dnsCache     *dnscache.Cache
	bypassPolicy *tunnel.BypassPolicy
	engine       *tunnel.Engine
	// cleanupErr retains the failed rollback for a repeated Disconnect call.
	cleanupErr        error
	state             lifecycleState
	deviceOpened      bool
	tunOwned          bool
	tunCloseAttempted bool
	tunCloseErr       error
	mu                sync.Mutex

	// connectCancel is deliberately separate from mu. Connect holds mu while
	// invoking the non-context-aware native Open/engine-start calls; requesting
	// cancellation must therefore only close this channel. Connect observes it
	// at native-operation boundaries and remains the sole owner of cleanup.
	cancelMu      sync.Mutex
	connectCancel chan struct{}
}

func newNativeRuntime(device protocol.ProtocolDevice, tun io.ReadWriteCloser, dnsCache *dnscache.Cache, bypass *tunnel.BypassPolicy) *nativeRuntime {
	c := &nativeRuntime{
		device:        device,
		tun:           tun,
		dnsCache:      dnsCache,
		bypassPolicy:  bypass,
		state:         stateIdle,
		connectCancel: make(chan struct{}),
	}
	log.Debugf(nativeLogCategory, "mobile session runtime created (tun2socks version)")
	return c
}

func (c *nativeRuntime) Connect(ctx context.Context) error {
	if c == nil {
		return errors.New("mobile session runtime is not initialized")
	}
	return runLockedWithPanicRecovery("mobile session connect", &c.mu, func() error { return c.connectLocked(ctx) }, func() error { return c.disconnectLocked(sessionapi.CleanupContext(ctx)) })
}

// CancelConnect requests cancellation without taking c.mu.  Connect owns the
// native startup operation and must perform all device/engine cleanup itself;
// calling Disconnect here would wait behind a blocked native Open and could
// release dependent resources from the wrong goroutine.
func (c *nativeRuntime) CancelConnect() {
	if c == nil {
		return
	}
	c.cancelMu.Lock()
	defer c.cancelMu.Unlock()
	if c.connectCancel == nil {
		c.connectCancel = make(chan struct{})
	}
	select {
	case <-c.connectCancel:
	default:
		close(c.connectCancel)
	}
}

func (c *nativeRuntime) connectWasCanceled() bool {
	c.cancelMu.Lock()
	cancel := c.connectCancel
	c.cancelMu.Unlock()
	if cancel == nil {
		return false
	}
	select {
	case <-cancel:
		return true
	default:
		return false
	}
}

func (c *nativeRuntime) connectLocked(ctx context.Context) (err error) {
	if c.state != stateIdle {
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
		cleanupErr := c.cleanupResourcesLocked(sessionapi.CleanupContext(ctx))
		c.cleanupErr = cleanupErr
		return errors.Join(cause, cleanupErr)
	}
	if c.connectWasCanceled() {
		return fail(errors.New("mobile session connect canceled before native startup"))
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
			log.Errorf(nativeLogCategory, "Set unix.SetNonblock error: %v", err)
		}
	} else {
		log.Errorf(nativeLogCategory, "failed to get FD from tun: descriptor unavailable")
		return fail(fmt.Errorf("TUN device does not expose a descriptor"))
	}

	c.deviceOpened = true
	err = c.device.Open(0, "")
	if err != nil {
		log.Errorf(nativeLogCategory, "failed to create protocol device: %v", err)
		return fail(fmt.Errorf("failed to open protocol device: %w", err))
	}
	c.deviceOpened = true
	if c.connectWasCanceled() {
		return fail(errors.New("mobile session connect canceled after protocol startup"))
	}

	log.Debugf(nativeLogCategory, "starting tun2socks engine proxy_ready=true")
	c.engine, err = tunnel.StartOwnedFDEngine(platform_engine.EngineConfig{
		ProxyAddr:   c.device.GetProxyAddr(),
		FD:          fd,
		UplinkIface: "",
	}, c.dnsCache, c.bypassPolicy)
	if err != nil {
		log.Debugf(nativeLogCategory, "Can't start tun2socks: %v", err)
		return fail(fmt.Errorf("failed to start tun2socks engine: %w", err))
	}
	if c.connectWasCanceled() {
		return fail(errors.New("mobile session connect canceled after tun2socks startup"))
	}

	if c.tun != nil {
		if closeErr := c.closeTunLocked(); closeErr != nil {
			log.Errorf(nativeLogCategory, "failed to close local tun fd wrapper after engine start: %v", closeErr)
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

func (c *nativeRuntime) cleanupResourcesLocked(ctx context.Context) error {
	if c.engine != nil {
		if err := c.engine.Stop(ctx); err != nil {
			return err
		}
		c.engine = nil
	}
	if c.deviceOpened && c.device != nil {
		if err := c.device.Close(); err != nil {
			return err
		}
		c.deviceOpened = false
	}
	if c.tunOwned && c.tun != nil {
		if err := c.closeTunLocked(); err != nil {
			return err
		}
		c.tunOwned = false
		c.tun = nil
	}
	return nil
}

func (c *nativeRuntime) Disconnect(ctx context.Context) error {
	if c == nil {
		return errors.New("mobile session runtime is not initialized")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.disconnectLocked(ctx)
}

// disconnectLocked performs cleanup while c.mu is held. Connect's panic
// recovery must use this form because its deferred recovery runs before
// Connect's deferred unlock.
func (c *nativeRuntime) disconnectLocked(ctx context.Context) error {
	if c.state == stateIdle {
		return nil
	}
	c.state = stateStopping

	err := c.cleanupResourcesLocked(sessionapi.CleanupContext(ctx))
	c.cleanupErr = err
	if err != nil {
		c.state = stateFailed
	} else {
		c.state = stateIdle
		c.engine, c.tun, c.device = nil, nil, nil
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
