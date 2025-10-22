//go:build android || ios

package outline

import (
	"fmt"
	"go_client/common"
	outlineCommon "go_client/outline/common"
	"go_client/outline/internal"
	"log"
	"net"
	// _ "go_client/logger"
)

type OutlineClient struct {
	device *internal.OutlineDevice
	config string
}

func NewClient(transportConfig string) *OutlineClient {
	c := &OutlineClient{config: transportConfig}
	log.Println("outline client created")
	common.Client.SetVpnClient(outlineCommon.Name, c)
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
	common.Client.MarkActive(outlineCommon.Name)
	return nil
}

func (c *OutlineClient) Disconnect() error {
	err := c.device.Close()
	if err != nil {
		log.Printf("failed to close outline device: %v\n", err)
		return err
	}
	log.Println("outline client disconnected")
	common.Client.MarkInactive(outlineCommon.Name)
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

<<<<<<< HEAD
<<<<<<< HEAD
<<<<<<< HEAD
<<<<<<< HEAD
	// TODO
=======
    // TODO
>>>>>>> cbe7829 (Return all buffer from android)
=======
>>>>>>> c62b141 (Try to return slice of buffer from android)
=======
    // TODO
>>>>>>> 42e0ee3 (Fix UI)
=======
	// TODO
>>>>>>> 7039ac7 (Rollback status marking)
	// Return a slice containing only the actually read bytes.
	// The TUN driver validates the capacity of the underlying buffer during write operations.
	// Returning the full 64KB buffer would cause "no buffer space available" errors
	// even if only a small portion contains actual data, because the TUN interface
	// has limited buffer capacity (typically 32KB on Android devices).
	return buf[:n], nil
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
