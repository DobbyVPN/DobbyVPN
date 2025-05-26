//go:build android || ios

package outline

import (
	"fmt"
	"go_client/outline/internal"
	"log"
	"net"
)

const Name = "outline"

type OutlineClient struct {
	device *internal.OutlineDevice
	config string
}

func NewClient(transportConfig string) *OutlineClient {
	c := &OutlineClient{config: transportConfig}
	log.Println("outline client created")
	//common.Client.SetVpnClient(Name, c)
	return c
}

func (c *OutlineClient) Connect() error {
	od, err := internal.NewOutlineDevice(c.config)
	if err != nil {
		log.Printf("failed to create outline device: %v\n", err)
		return err
	}

	log.Println("outline device created")
	log.Println("outline client connected")

	c.device = od
	//common.Client.MarkActive(Name)
	return nil
}

func (c *OutlineClient) Disconnect() error {
	err := c.device.Close()
	if err != nil {
		log.Printf("failed to close outline device: %v\n", err)
		return err
	}
	log.Println("outline client disconnected")
	//common.Client.MarkInactive(Name)
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
	n, err := c.device.Read(buf)
	log.Println(fmt.Sprintf("outline client: read data; size: %d (%d)", n, n%8))
	if err != nil {
		log.Printf("failed to read data: %v\n", err)
		return nil, fmt.Errorf("failed to read data: %w", err)
	}
	return buf, nil
}

func (c *OutlineClient) Write(buf []byte) (int, error) {
	n, err := c.device.Write(buf)
	log.Println(fmt.Sprintf("outline client: write data; size: %d (%d)", n, n%8))
	if err != nil {
		log.Printf("failed to write data: %v\n", err)
		return 0, fmt.Errorf("failed to write data: %w", err)
	}
	return n, nil
}
