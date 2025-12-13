//go:build android || ios

package outline

import (
	"fmt"
	"go_client/common"
	log "go_client/logger"
	outlineCommon "go_client/outline/common"
	"go_client/outline/internal"
	"net"
)

type OutlineClient struct {
	device *internal.OutlineDevice
	config string
}

func NewClient(transportConfig string) *OutlineClient {
	c := &OutlineClient{config: transportConfig}
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
	return nil
}

func (c *OutlineClient) Disconnect() error {
	if c.device == nil {
		log.Infof("outline device is nil, nothing to disconnect")
		common.Client.MarkInactive(outlineCommon.Name)
		return nil
	}
	err := c.device.Close()
	if err != nil {
		log.Infof("failed to close outline device: %v\n", err)
		return err
	}
	log.Infof("outline client disconnected")
	common.Client.MarkInactive(outlineCommon.Name)
	return nil
}

func (c *OutlineClient) Refresh() error {
	if c.device == nil {
		return fmt.Errorf("outline device is not initialized")
	}
	return c.device.Refresh()
}

func (c *OutlineClient) GetServerIP() net.IP {
	if c.device == nil {
		return nil
	}
	return c.device.GetServerIP()
}

func (c *OutlineClient) Read() ([]byte, error) {
	if c.device == nil {
		return nil, fmt.Errorf("outline device is not initialized")
	}
	buf := make([]byte, 65536)
	n, err := c.device.Read(buf)
	//log.Infof(fmt.Sprintf("outline client: read data; size: %d (%d)", n, n%8))
	if err != nil {
		log.Infof("failed to read data: %v\n", err)
		return nil, fmt.Errorf("failed to read data: %w", err)
	}

	// TODO
	// Return a slice containing only the actually read bytes.
	// The TUN driver validates the capacity of the underlying buffer during write operations.
	// Returning the full 64KB buffer would cause "no buffer space available" errors
	// even if only a small portion contains actual data, because the TUN interface
	// has limited buffer capacity (typically 32KB on Android devices).
	return buf[:n], nil
}

func (c *OutlineClient) Write(buf []byte) (int, error) {
	if c.device == nil {
		return 0, fmt.Errorf("outline device is not initialized")
	}
	n, err := c.device.Write(buf)
	//log.Infof(fmt.Sprintf("outline client: write data; size: %d (%d)", n, n%8))
	if err != nil {
		log.Infof("failed to write data: %v\n", err)
		return 0, fmt.Errorf("failed to write data: %w", err)
	}
	return n, nil
}
