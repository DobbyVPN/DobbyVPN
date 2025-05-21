//go:build android || ios

package outline

import (
	"fmt"
	"go_client/common"
	"go_client/outline/internal"
	"net"

	_ "go_client/logger"
)

const Name = "outline"

type OutlineClient struct {
	device *internal.OutlineDevice
	config string
}

func NewClient(transportConfig string) *OutlineClient {
	c := &OutlineClient{config: transportConfig}
	// log.Infof("outline client created")
	common.Client.SetVpnClient(Name, c)
	return c
}

func (c *OutlineClient) Connect() error {
	od, err := internal.NewOutlineDevice(c.config)
	if err != nil {
		// log.Errorf("failed to create outline device: %v", err)
		return err
	}

	// log.Infof("outline device created")
	// log.Infof("outline client connected")

	c.device = od
	common.Client.MarkActive(Name)
	return nil
}

func (c *OutlineClient) Disconnect() error {
	err := c.device.Close()
	if err != nil {
		// log.Errorf("failed to close outline device: %v", err)
		return err
	}
	// log.Infof("outline client disconnected")
	common.Client.MarkInactive(Name)
	return nil
}

func (c *OutlineClient) Refresh() error {
	return c.device.Refresh()
}

func (c *OutlineClient) GetServerIP() net.IP {
	return c.device.GetServerIP()
}

func (c *OutlineClient) Read() ([]byte, error) {
	buf := make([]byte, 65536)
	// log.Infof("outline client: read data")
	n, err := c.device.Read(buf)
	if err != nil {
		// log.Errorf("failed to read data: %v", err)
		return nil, fmt.Errorf("failed to read data: %w", err)
	}
	return buf[:n], nil
}

func (c *OutlineClient) Write(buf []byte) (int, error) {
	// log.Infof("outline client: write data")
	n, err := c.device.Write(buf)
	if err != nil {
		// log.Errorf("failed to write data: %v", err)
		return 0, fmt.Errorf("failed to write data: %w", err)
	}
	return n, nil
}
