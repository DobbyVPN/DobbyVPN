//go:build android || ios

package outline

import (
	"fmt"
	"go_client/common"
	"go_client/log"
	outlineCommon "go_client/outline/common"
	"go_client/outline/internal"
	"go_client/tunnel"
	"golang.org/x/sys/unix"
	"io"
	"net"
	"os"
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

	log.Infof("starting tun2socks engine with proxy %s", od.GetProxyAddr())
	tunnel.StartEngineLinuxBased(fd, od.GetProxyAddr())

	common.Client.MarkActive(outlineCommon.Name)
	log.Infof("outline client connected successfully via tun2socks")
	return nil
}

func (c *OutlineClient) Disconnect() error {
	tunnel.StopEngine()

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
