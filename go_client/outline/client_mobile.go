//go:build android || ios

package outline

import (
	"go_client/common"
	log "go_client/logger"
	outlineCommon "go_client/outline/common"
	"go_client/outline/internal"
	"net"
)

type OutlineClient struct {
	device *internal.OutlineDevice
	fd     int
	config string
}

func NewClient(transportConfig string, fd int) *OutlineClient {
	c := &OutlineClient{config: transportConfig, fd: fd}
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
	common.StartTransfer(
		c.fd,
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
	err := c.device.Close()
	if err != nil {
		log.Infof("failed to close outline device: %v\n", err)
		return err
	}
	common.StopTransfer()
	log.Infof("outline client disconnected")
	common.Client.MarkInactive(outlineCommon.Name)
	return nil
}

func (c *OutlineClient) Refresh() error {
	return c.device.Refresh()
}

func (c *OutlineClient) GetServerIP() net.IP {
	return c.device.GetServerIP()
}
