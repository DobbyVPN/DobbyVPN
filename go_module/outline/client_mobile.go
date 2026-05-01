//go:build android || ios

package outline

import (
	"fmt"
	"go_module/common"
	"go_module/log"
	outlineCommon "go_module/outline/common"
	"go_module/outline/internal"
	"go_module/tunnel"
	"go_module/tunnel/platform_engine"
	"golang.org/x/sys/unix"
	"io"
	"net"
	"os"
)

type OutlineClient struct {
	device        *internal.OutlineDevice
	tunFD         int
	engineStarted bool
	config        string
	mtu           int
}

func NewClient(transportConfig string, tun io.ReadWriteCloser) *OutlineClient {
	return NewClientWithMTU(transportConfig, tun, 0)
}

func NewClientWithMTU(transportConfig string, tun io.ReadWriteCloser, mtu int) *OutlineClient {
	fd := -1
	if f, ok := tun.(*os.File); ok {
		fd = int(f.Fd())
	} else {
		log.Infof("failed to get FD from tun: not an *os.File")
	}
	return NewClientWithFD(transportConfig, fd, mtu)
}

func NewClientWithFD(transportConfig string, fd int, mtu int) *OutlineClient {
	if mtu <= 0 {
		mtu = 1200
	}
	c := &OutlineClient{
		config: transportConfig,
		tunFD:  fd,
		mtu:    mtu,
	}
	log.Infof("outline client created (tun2socks version)")
	common.Client.SetVpnClient(outlineCommon.Name, c)
	return c
}

func (c *OutlineClient) Connect() error {
	defer func() {
		if r := recover(); r != nil {
			log.Infof("RECOVERED from fail in Connect: %v", r)
		}
	}()
	od, err := internal.NewOutlineDevice(c.config)
	if err != nil {
		log.Infof("failed to create outline device: %v\n", err)
		return err
	}

	log.Infof("outline device (SOCKS5 bridge) created")
	c.device = od

	fd := c.tunFD
	if fd < 0 {
		return fmt.Errorf("invalid tun fd")
	}
	err = unix.SetNonblock(fd, true)
	if err != nil {
		log.Infof("Set unix.SetNonblock error: %v", err)
	}

	log.Infof("starting tun2socks engine with proxy %s", od.GetProxyAddr())
	err = tunnel.StartEngine(platform_engine.EngineConfig{
		ProxyAddr:   od.GetProxyAddr(),
		FD:          fd,
		UplinkIface: "",
		MTU:         c.mtu,
	})
	if err != nil {
		log.Infof("Can't start tun2socks: %v", err)
		return err
	}
	c.engineStarted = true

	common.Client.MarkActive(outlineCommon.Name)
	log.Infof("outline client connected successfully via tun2socks")
	return nil
}

func (c *OutlineClient) Disconnect() error {
	tunnel.StopEngine()
	if !c.engineStarted && c.tunFD >= 0 {
		if err := unix.Close(c.tunFD); err != nil {
			log.Infof("failed to close unused tun fd: %v\n", err)
		}
	}
	c.engineStarted = false
	c.tunFD = -1

	if c.device != nil {
		if err := c.device.Close(); err != nil {
			log.Infof("failed to close outline device: %v\n", err)
		}
		c.device = nil
	}

	log.Infof("outline client disconnected")
	common.Client.MarkInactive(outlineCommon.Name)
	return nil
}

func (c *OutlineClient) Refresh() error {
	return nil
}

func (c *OutlineClient) GetServerIP() net.IP {
	if c.device == nil {
		return nil
	}
	return c.device.GetServerIP()
}
