//go:build android || ios

package outline

import (
	"go_client/common"
	log "go_client/logger"
	outlineCommon "go_client/outline/common"
	"go_client/outline/internal"
	"go_client/tunnel"
	"io"
	"net"
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
	log.Infof("outline client created")
	common.Client.SetVpnClient(outlineCommon.Name, c)
	return c
}

func (c *OutlineClient) Connect() error {
	od, err := internal.NewOutlineDevice(c.config)
	if err != nil {
		log.Infof("failed to create outline device: %v\n", err)
		return err
	}

	log.Infof("outline device created")
	log.Infof("outline client connected")

	c.device = od
	common.Client.MarkActive(outlineCommon.Name)

	log.Infof("start read/write goroutines")

	tunnel.StartTransfer(
		c.tun,
		func(buf []byte) (int, error) {
			return c.device.Read(buf)
		},
		func(buf []byte) (int, error) {
			return c.device.Write(buf)
		},
	)

	return nil
}

func (c *OutlineClient) Disconnect() error {
	if c.device != nil {
		if err := c.device.Close(); err != nil {
			log.Infof("failed to close outline device: %v\n", err)
			return err
		}
	}

	tunnel.StopTransfer()

	log.Infof("outline client disconnected")
	common.Client.MarkInactive(outlineCommon.Name)
	return nil
}

func (c *OutlineClient) Refresh() error {
	if c.device == nil {
		return nil
	}
	return c.device.Refresh()
}

func (c *OutlineClient) GetServerIP() net.IP {
	if c.device == nil {
		return nil
	}
	return c.device.GetServerIP()
}
