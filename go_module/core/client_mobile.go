//go:build android || ios

package core

import (
	"errors"
	"fmt"
	"go_module/common"
	coreCommon "go_module/core/common"
	"go_module/core/pkg"
	"go_module/log"
	"go_module/tunnel"
	"go_module/tunnel/platform_engine"
	"io"
	"net"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

type CoreClient struct {
	device     pkg.ProtocolDevice
	tun        io.ReadWriteCloser
	engine     *tunnel.Engine
	resources  *resourceLedger
	state      LifecycleState
	generation uint64
	mu         sync.Mutex
}

func NewClient(device pkg.ProtocolDevice, tun io.ReadWriteCloser) *CoreClient {
	c := &CoreClient{
		device: device,
		tun:    tun,
		state:  StateIdle,
	}
	log.Debugf(coreCommon.Category, "core mobile client created (tun2socks version)")
	common.Client.SetVpnClient(coreCommon.Name, c)
	return c
}

func (c *CoreClient) Connect() (err error) {
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
		common.Client.MarkInactive(coreCommon.Name)
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
	if f, ok := c.tun.(*os.File); ok {
		fd = int(f.Fd())
		err := unix.SetNonblock(fd, true)
		if err != nil {
			log.Debugf(coreCommon.Category, "Set unix.SetNonblock error: %v", err)
		}
	} else {
		log.Debugf(coreCommon.Category, "failed to get FD from tun: not an *os.File")
		return fail(fmt.Errorf("invalid tun device type"))
	}

	engineFD, err := unix.Dup(fd)
	if err != nil {
		log.Debugf(coreCommon.Category, "failed to duplicate tun fd for tun2socks: %v", err)
		return fail(fmt.Errorf("failed to duplicate tun fd for tun2socks: %w", err))
	}
	releaseFD := ledger.Add(func() error { return unix.Close(engineFD) })

	err = c.device.Open(0, "")
	if err != nil {
		log.Debugf(coreCommon.Category, "failed to create protocol device: %v", err)
		return fail(fmt.Errorf("failed to open protocol device: %w", err))
	}
	ledger.Add(c.device.Close)

	log.Debugf(coreCommon.Category, "starting tun2socks engine with proxy %s", c.device.GetProxyAddr())
	c.engine, err = tunnel.StartOwnedEngine(platform_engine.EngineConfig{
		ProxyAddr:   c.device.GetProxyAddr(),
		FD:          engineFD,
		UplinkIface: "",
	})
	if err != nil {
		log.Debugf(coreCommon.Category, "Can't start tun2socks: %v", err)
		return fail(fmt.Errorf("failed to start tun2socks engine: %w", err))
	}
	releaseFD() // tun2socks now owns the duplicated descriptor.
	ledger.Add(func() error { c.engine.Stop(); return nil })

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
	common.Client.MarkActive(coreCommon.Name)
	log.Debugf(coreCommon.Category, "core client connected successfully via tun2socks")
	return nil
}

func (c *CoreClient) Disconnect() error {
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

	log.Debugf(coreCommon.Category, "core client disconnected")
	common.Client.MarkInactive(coreCommon.Name)
	return err
}

func (c *CoreClient) SwitchDevice(device pkg.ProtocolDevice) error {
	if c == nil {
		return errors.New("core mobile client is not initialized")
	}
	if device == nil {
		return errors.New("core mobile protocol device is not initialized")
	}

	return fmt.Errorf("protocol changes require a completed disconnect before starting a new session")
}

func (c *CoreClient) Refresh() error {
	return fmt.Errorf("core client refresh is unsupported; stop and start a new session")
}

func (c *CoreClient) HealthCheck() error {
	return nil
}

func (c *CoreClient) GetServerIP() net.IP {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c == nil || c.device == nil {
		return nil
	}
	return c.device.GetServerIP()
}

// State returns the current lifecycle state without exposing mutable resources.
func (c *CoreClient) State() LifecycleState {
	if c == nil {
		return StateFailed
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Generation changes for every accepted start attempt.
func (c *CoreClient) Generation() uint64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generation
}
