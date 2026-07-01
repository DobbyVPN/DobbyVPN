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
	device pkg.ProtocolDevice
	tun    io.ReadWriteCloser
	mu     sync.Mutex
}

func NewClient(device pkg.ProtocolDevice, tun io.ReadWriteCloser) *CoreClient {
	c := &CoreClient{
		device: device,
		tun:    tun,
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
		}
	}()

	if c == nil {
		return errors.New("core mobile client is not initialized")
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.device == nil {
		return errors.New("core mobile protocol device is not initialized")
	}
	if c.tun == nil {
		common.Client.MarkActive(coreCommon.Name)
		log.Debugf(coreCommon.Category, "core mobile client already connected; skipping tun2socks engine start")
		return nil
	}

	var fd int
	if f, ok := c.tun.(*os.File); ok {
		fd = int(f.Fd())
		err := unix.SetNonblock(fd, true)
		if err != nil {
			log.Debugf(coreCommon.Category, "Set unix.SetNonblock error: %v", err)
		}
	} else {
		log.Debugf(coreCommon.Category, "failed to get FD from tun: not an *os.File")
		return fmt.Errorf("invalid tun device type")
	}

	engineFD, err := unix.Dup(fd)
	if err != nil {
		log.Debugf(coreCommon.Category, "failed to duplicate tun fd for tun2socks: %v", err)
		common.Client.MarkInactive(coreCommon.Name)
		return fmt.Errorf("failed to duplicate tun fd for tun2socks: %w", err)
	}
	engineStarted := false
	defer func() {
		if !engineStarted {
			_ = unix.Close(engineFD)
		}
	}()

	err = c.device.Open(0, "")
	if err != nil {
		log.Debugf(coreCommon.Category, "failed to create protocol device: %v", err)
		common.Client.MarkInactive(coreCommon.Name)
		return fmt.Errorf("failed to open protocol device: %w", err)
	}

	log.Debugf(coreCommon.Category, "starting tun2socks engine with proxy %s", c.device.GetProxyAddr())
	err = tunnel.StartEngine(platform_engine.EngineConfig{
		ProxyAddr:   c.device.GetProxyAddr(),
		FD:          engineFD,
		UplinkIface: "",
	})
	if err != nil {
		log.Debugf(coreCommon.Category, "Can't start tun2socks: %v", err)
		if c.tun != nil {
			if closeErr := c.tun.Close(); closeErr != nil {
				log.Debugf(coreCommon.Category, "failed to close tun fd after tun2socks start error: %v", closeErr)
				err = errors.Join(err, fmt.Errorf("failed to close tun fd after tun2socks start error: %w", closeErr))
			}
			c.tun = nil
		}
		if closeErr := c.device.Close(); closeErr != nil {
			log.Debugf(coreCommon.Category, "failed to close protocol device after tun2socks start error: %v", closeErr)
			err = errors.Join(err, fmt.Errorf("failed to close protocol device after tun2socks start error: %w", closeErr))
		}
		c.device = nil
		common.Client.MarkInactive(coreCommon.Name)
		return fmt.Errorf("failed to start tun2socks engine: %w", err)
	}
	engineStarted = true

	if c.tun != nil {
		if closeErr := c.tun.Close(); closeErr != nil {
			log.Debugf(coreCommon.Category, "failed to close local tun fd wrapper after engine start: %v", closeErr)
		} else {
			log.Debugf(coreCommon.Category, "local tun fd wrapper closed after engine start")
		}
		c.tun = nil
	}

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

	var errs []error
	tunnel.StopEngine()

	if c.tun != nil {
		if err := c.tun.Close(); err != nil {
			log.Debugf(coreCommon.Category, "failed to close unused tun fd wrapper: %v", err)
			errs = append(errs, fmt.Errorf("failed to close unused tun fd wrapper: %w", err))
		}
		c.tun = nil
	}

	if c.device != nil {
		if err := c.device.Close(); err != nil {
			log.Debugf(coreCommon.Category, "failed to close protocol device: %v", err)
			errs = append(errs, fmt.Errorf("failed to close protocol device: %w", err))
		}
		c.device = nil
	}

	log.Debugf(coreCommon.Category, "core client disconnected")
	common.Client.MarkInactive(coreCommon.Name)
	return errors.Join(errs...)
}

func (c *CoreClient) SwitchDevice(device pkg.ProtocolDevice) error {
	if c == nil {
		return errors.New("core mobile client is not initialized")
	}
	if device == nil {
		return errors.New("core mobile protocol device is not initialized")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.device == nil {
		return errors.New("core mobile current protocol device is not initialized")
	}

	if err := device.Open(0, ""); err != nil {
		return fmt.Errorf("failed to open replacement protocol device: %w", err)
	}

	proxyAddr := device.GetProxyAddr()
	if err := tunnel.SwitchVPNProxy(proxyAddr); err != nil {
		_ = device.Close()
		return fmt.Errorf("failed to switch tun2socks proxy to replacement device: %w", err)
	}

	oldDevice := c.device
	c.device = device
	common.Client.MarkActive(coreCommon.Name)
	log.Debugf(coreCommon.Category, "core mobile client switched protocol device proxy=%s", proxyAddr)

	go func() {
		if err := oldDevice.Close(); err != nil {
			log.Debugf(coreCommon.Category, "failed to close previous protocol device after switch: %v", err)
		}
	}()
	return nil
}

func (c *CoreClient) Refresh() error {
	return nil
}

func (c *CoreClient) HealthCheck() error {
	return nil
}

func (c *CoreClient) GetServerIP() net.IP {
	if c == nil || c.device == nil {
		return nil
	}
	return c.device.GetServerIP()
}
