//go:build android || ios

package core

import (
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

	"golang.org/x/sys/unix"
)

type CoreClient struct {
	device pkg.ProtocolDevice
	tun    io.ReadWriteCloser
}

func NewClient(device pkg.ProtocolDevice, tun io.ReadWriteCloser) *CoreClient {
	c := &CoreClient{
		device: device,
		tun:    tun,
	}
	log.Infof("core mobile client created (tun2socks version)")
	common.Client.SetVpnClient(coreCommon.Name, c)
	return c
}

func (c *CoreClient) Connect() error {
	defer func() {
		if r := recover(); r != nil {
			log.Infof("RECOVERED from fail in Connect: %v", r)
		}
	}()

	var fd int
	if f, ok := c.tun.(*os.File); ok {
		fd = int(f.Fd())
		err := unix.SetNonblock(fd, true)
		if err != nil {
			log.Infof("Set unix.SetNonblock error: %v", err)
		}
	} else {
		log.Infof("failed to get FD from tun: not an *os.File")
		return fmt.Errorf("invalid tun device type")
	}

	err := c.device.Open(0, "")
	if err != nil {
		log.Infof("failed to create protocol device: %v", err)
		common.Client.MarkInactive(coreCommon.Name)
		return err
	}

	log.Infof("starting tun2socks engine with proxy %s", c.device.GetProxyAddr())
	err = tunnel.StartEngine(platform_engine.EngineConfig{
		ProxyAddr:   c.device.GetProxyAddr(),
		FD:          fd,
		UplinkIface: "",
	})
	if err != nil {
		log.Infof("Can't start tun2socks: %v", err)
		return err
	}

	common.Client.MarkActive(coreCommon.Name)
	log.Infof("core client connected successfully via tun2socks")
	return nil
}

func (c *CoreClient) Disconnect() error {
	tunnel.StopEngine()

	if c.device != nil {
		if err := c.device.Close(); err != nil {
			log.Infof("failed to close outline device: %v\n", err)
		}
		c.device = nil
	}

	log.Infof("outline client disconnected")
	common.Client.MarkInactive(coreCommon.Name)
	return nil
}

func (c *CoreClient) Refresh() error {
	return nil
}

func (c *CoreClient) GetServerIP() net.IP {
	return c.device.GetServerIP()
}
