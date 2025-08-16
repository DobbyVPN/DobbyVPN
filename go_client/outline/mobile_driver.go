//go:build android || ios

package outline

import (
	"fmt"
	"go_client/common"
	"go_client/outline/internal"
	"log"
	"net"
	//_ "go_client/logger"
)

const Name = "outline"

type mobileDriver struct {
	device *internal.OutlineDevice
	config string
}

func newDriver(transportConfig string) Driver {
	return &mobileDriver{config: transportConfig}
}

func (d *mobileDriver) Connect() error {
	od, err := internal.NewOutlineDevice(d.config)
	if err != nil {
		log.Printf("failed to create outline device: %v\n", err)
		return err
	}

	log.Println("outline device created")
	log.Println("outline client connected")

	d.device = od
	common.Client.MarkActive(Name)
	return nil
}

func (d *mobileDriver) Disconnect() error {
	err := d.device.Close()
	if err != nil {
		log.Printf("failed to close outline device: %v\n", err)
		return err
	}
	log.Println("outline client disconnected")
	common.Client.MarkInactive(Name)
	return nil
}

func (d *mobileDriver) Refresh() error {
	return d.device.Refresh()
}

func (d *mobileDriver) GetServerIP() net.IP {
	return d.device.GetServerIP()
}

func (d *mobileDriver) Read(buf []byte) (int, error) {
	n, err := d.device.Read(buf)
	log.Println(fmt.Sprintf("outline client: read data; size: %d (%d)", n, n%8))
	if err != nil {
		log.Printf("failed to read data: %v\n", err)
		return 0, fmt.Errorf("failed to read data: %w", err)
	}
	return n, nil
}

func (d *mobileDriver) Write(buf []byte) (int, error) {
	n, err := d.device.Write(buf)
	log.Println(fmt.Sprintf("outline client: write data; size: %d (%d)", n, n%8))
	if err != nil {
		log.Printf("failed to write data: %v\n", err)
		return 0, fmt.Errorf("failed to write data: %w", err)
	}
	return n, nil
}
