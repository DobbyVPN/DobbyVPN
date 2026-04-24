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
	"io"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

type OutlineClient struct {
	device *internal.OutlineDevice
	tun    io.ReadWriteCloser
	config string
}

func NewClient(transportConfig string, tun io.ReadWriteCloser) *OutlineClient {
	c := &OutlineClient{
		config: transportConfig,
		tun:    tun,
	}
	log.SimpleInfof(Category, "outline client created (tun2socks version)")
	common.Client.SetVpnClient(outlineCommon.Name, c)
	return c
}

func (c *OutlineClient) Connect() error {
	defer func() {
		if r := recover(); r != nil {
			log.SimpleWarnf(Category, "RECOVERED from fail in Connect: %v", r)
		}
	}()
	od, err := internal.NewOutlineDevice(c.config)
	if err != nil {
		log.SimpleErrorf(Category, "failed to create outline device: %v\n", err)
		return err
	}

	log.SimpleDebugf(Category, "outline device (SOCKS5 bridge) created")
	c.device = od

	var fd int
	if f, ok := c.tun.(*os.File); ok {
		fd = int(f.Fd())
		err := unix.SetNonblock(fd, true)
		if err != nil {
			log.SimpleWarnf(Category, "Set unix.SetNonblock error: %v", err)
		}
	} else {
		log.SimpleErrorf(Category, "failed to get FD from tun: not an *os.File")
		return fmt.Errorf("invalid tun device type")
	}

	log.SimpleDebugf(Category, "starting tun2socks engine with proxy %s", od.GetProxyAddr())
	err = tunnel.StartEngine(platform_engine.EngineConfig{
		ProxyAddr:   od.GetProxyAddr(),
		FD:          fd,
		UplinkIface: "",
	})
	if err != nil {
		log.SimpleErrorf(Category, "Can't start tun2socks: %v", err)
		return err
	}

	common.Client.MarkActive(outlineCommon.Name)
	log.SimpleInfof(Category, "outline client connected successfully via tun2socks")
	return nil
}

func (c *OutlineClient) Disconnect() error {
	tunnel.StopEngine()

	if c.device != nil {
		if err := c.device.Close(); err != nil {
			log.SimpleWarnf(Category, "failed to close outline device: %v\n", err)
		}
		c.device = nil
	}

	log.SimpleInfof(Category, "outline client disconnected")
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
