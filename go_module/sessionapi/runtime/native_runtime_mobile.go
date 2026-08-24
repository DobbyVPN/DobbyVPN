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
// sessionapi/runtime. SessionV2 owns the externally meaningful session state
// and generation; this type only tracks local resource cleanup.
type nativeRuntime struct {
	device    protocol.ProtocolDevice
	tun       io.ReadWriteCloser
	engine    *tunnel.Engine
	resources *resourceLedger
	// cleanupErr makes an incomplete rollback a terminal generation failure;
	// starting a new generation while native resources may still be live is
	// unsafe and must fail closed.
	cleanupErr error
	state      lifecycleState
	generation uint64
	mu         sync.Mutex
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

// closeMobileTunAfterEngine closes the wrapper after tun2socks has taken
// ownership of the descriptor. The ledger entry is released only after the
// close succeeds; on failure the caller must roll back and retain the failed
// cleanup in the generation state.
func closeMobileTunAfterEngine(closeTun func() error, release func()) error {
	if closeTun == nil {
		return errors.New("mobile TUN close function is not initialized")
	}
	if err := closeTun(); err != nil {
		return fmt.Errorf("failed to close local TUN after engine start: %w", err)
	}
	if release != nil {
		release()
	}
	return nil
}

func (c *nativeRuntime) Connect() error {
	if c == nil {
		return errors.New("mobile session runtime is not initialized")
	}
	return runLockedWithPanicRecovery("mobile session connect", &c.mu, c.connectLocked, c.disconnectLocked)
}

func (c *nativeRuntime) connectLocked() (err error) {
	if c.state == stateFailed && c.cleanupErr != nil {
		return fmt.Errorf("native session runtime has incomplete cleanup: %w", c.cleanupErr)
	}
	if c.state != stateIdle && c.state != stateFailed {
		return lifecycleBusyError(c.state)
	}
	c.generation++
	c.state = statePreparing
	ledger := &resourceLedger{}
	c.resources = ledger
	c.cleanupErr = nil
	fail := func(cause error) error {
		c.state = stateFailed
		cleanupErr := ledger.Rollback()
		c.cleanupErr = cleanupErr
		c.engine = nil
		c.tun = nil
		c.device = nil
		return errors.Join(cause, cleanupErr)
	}

	if c.device == nil {
		return fail(errors.New("mobile protocol device is not initialized"))
	}
	if c.tun == nil {
		return fail(errors.New("mobile TUN device is not initialized"))
	}
	var tunCloseOnce sync.Once
	var tunCloseErr error
	closeTun := func() error {
		tunCloseOnce.Do(func() {
			tunCloseErr = c.tun.Close()
		})
		return tunCloseErr
	}
	releaseTun := ledger.Add(closeTun)

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
	ledger.Add(c.device.Close)

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
	ownedEngine := c.engine
	ledger.Add(ownedEngine.Stop)

	if c.tun != nil {
		if closeErr := closeMobileTunAfterEngine(closeTun, releaseTun); closeErr != nil {
			log.Debugf(nativeLogCategory, "failed to close local tun fd wrapper after engine start: %v", closeErr)
			return fail(closeErr)
		}
		log.Debugf(nativeLogCategory, "local tun fd wrapper closed after engine start")
		c.tun = nil
	}

	c.state = stateConnected
	log.Debugf(nativeLogCategory, "native session runtime connected successfully via tun2socks")
	return nil
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

	var err error
	if c.resources != nil {
		err = c.resources.Rollback()
	}
	c.cleanupErr = err
	c.resources = nil
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

func (c *nativeRuntime) generationValue() uint64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generation
}
