//go:build android || ios

package core

import (
	"errors"
	"fmt"
	coreCommon "go_module/core/common"
	"go_module/core/pkg"
	"go_module/log"
	"go_module/tunnel"
	"go_module/tunnel/platform_engine"
	"io"
	"net"
	"sync"

	"golang.org/x/sys/unix"
)

type SessionRuntime struct {
	device     pkg.ProtocolDevice
	tun        io.ReadWriteCloser
	engine     *tunnel.Engine
	resources  *resourceLedger
	state      LifecycleState
	generation uint64
	mu         sync.Mutex
}

func NewSession(device pkg.ProtocolDevice, tun io.ReadWriteCloser) *SessionRuntime {
	c := &SessionRuntime{
		device: device,
		tun:    tun,
		state:  StateIdle,
	}
	log.Debugf(coreCommon.Category, "core mobile client created (tun2socks version)")
	return c
}

func (c *SessionRuntime) Connect() (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Debugf(coreCommon.Category, "RECOVERED from fail in Connect: %v", r)
			err = fmt.Errorf("core mobile connect panic: %v", r)
			_ = c.Disconnect()
		}
	}()

	if c == nil {
		return errors.New("core mobile client is not initialized")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != StateIdle && c.state != StateFailed {
		return lifecycleBusyError(c.state)
	}
	c.generation++
	c.state = StatePreparing
	ledger := &resourceLedger{}
	c.resources = ledger
	fail := func(cause error) error {
		c.state = StateFailed
		cleanupErr := ledger.Rollback()
		c.engine = nil
		c.tun = nil
		c.device = nil
		return errors.Join(cause, cleanupErr)
	}

	if c.device == nil {
		return fail(errors.New("core mobile protocol device is not initialized"))
	}
	if c.tun == nil {
		return fail(errors.New("core mobile TUN device is not initialized"))
	}
	releaseTun := ledger.Add(c.tun.Close)

	var fd int
	if f, ok := c.tun.(interface{ Fd() uintptr }); ok {
		fd = int(f.Fd())
		err := unix.SetNonblock(fd, true)
		if err != nil {
			log.Debugf(coreCommon.Category, "Set unix.SetNonblock error: %v", err)
		}
	} else {
		log.Debugf(coreCommon.Category, "failed to get FD from tun: descriptor unavailable")
		return fail(fmt.Errorf("TUN device does not expose a descriptor"))
	}

	err = c.device.Open(0, "")
	if err != nil {
		log.Debugf(coreCommon.Category, "failed to create protocol device: %v", err)
		return fail(fmt.Errorf("failed to open protocol device: %w", err))
	}
	ledger.Add(c.device.Close)

	log.Debugf(coreCommon.Category, "starting tun2socks engine proxy_ready=true")
	c.engine, err = tunnel.StartOwnedFDEngine(platform_engine.EngineConfig{
		ProxyAddr:   c.device.GetProxyAddr(),
		FD:          fd,
		UplinkIface: "",
	})
	if err != nil {
		log.Debugf(coreCommon.Category, "Can't start tun2socks: %v", err)
		return fail(fmt.Errorf("failed to start tun2socks engine: %w", err))
	}
	ownedEngine := c.engine
	ledger.Add(ownedEngine.Stop)

	if c.tun != nil {
		if closeErr := c.tun.Close(); closeErr != nil {
			log.Debugf(coreCommon.Category, "failed to close local tun fd wrapper after engine start: %v", closeErr)
		} else {
			log.Debugf(coreCommon.Category, "local tun fd wrapper closed after engine start")
		}
		c.tun = nil
	}
	releaseTun()

	c.state = StateConnected
	log.Debugf(coreCommon.Category, "native session runtime connected successfully via tun2socks")
	return nil
}

func (c *SessionRuntime) Disconnect() error {
	if c == nil {
		return errors.New("core mobile client is not initialized")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == StateIdle {
		return nil
	}
	c.state = StateStopping

	err := c.resources.Rollback()
	c.resources = nil
	c.engine = nil
	c.tun = nil
	c.device = nil
	c.state = StateIdle

	log.Debugf(coreCommon.Category, "native session runtime disconnected")
	return err
}

func (c *SessionRuntime) SwitchDevice(device pkg.ProtocolDevice) error {
	if c == nil {
		return errors.New("core mobile client is not initialized")
	}
	if device == nil {
		return errors.New("core mobile protocol device is not initialized")
	}

	return fmt.Errorf("protocol changes require a completed disconnect before starting a new session")
}

func (c *SessionRuntime) Refresh() error {
	return fmt.Errorf("native session runtime refresh is unsupported; stop and start a new session")
}

func (c *SessionRuntime) GetServerIP() net.IP {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c == nil || c.device == nil {
		return nil
	}
	return c.device.GetServerIP()
}

// State returns the current lifecycle state without exposing mutable resources.
func (c *SessionRuntime) State() LifecycleState {
	if c == nil {
		return StateFailed
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Generation changes for every accepted start attempt.
func (c *SessionRuntime) Generation() uint64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generation
}
