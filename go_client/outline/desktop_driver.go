//go:build !(android || ios)

package outline

import (
	"context"
	log "github.com/sirupsen/logrus"
	"go_client/common"
	"go_client/outline/internal"
	"net"
	"sync"
)

const Name = "outline"

type desktopDriver struct {
	app    *internal.App
	cancel func()

	mu sync.Mutex
}

func newDriver(transportConfig string) Driver {
	return &desktopDriver{
		app: &internal.App{
			TransportConfig: &transportConfig,
			RoutingConfig: &internal.RoutingConfig{
				TunDeviceName:        "outline233",
				TunDeviceIP:          "10.233.233.1",
				TunDeviceMTU:         1500,
				TunGatewayCIDR:       "10.233.233.2/32",
				RoutingTableID:       233,
				RoutingTablePriority: 23333,
				DNSServerIP:          "9.9.9.9",
			},
		},
	}
}

func (d *desktopDriver) Connect() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel

	go func() {
		if err := d.app.Run(ctx); err != nil {
			log.Errorf("connect outline failed: %v", err)
			common.Client.MarkInactive(Name)
		}
	}()

	common.Client.MarkActive(Name)
	return nil
}

func (d *desktopDriver) Disconnect() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}
	common.Client.MarkInactive(Name)
	return nil
}

func (d *desktopDriver) Refresh() error {
	_ = d.Disconnect()
	return d.Connect()
}

func (d *desktopDriver) Read(buf []byte) (int, error) {
	return 0, nil
}

func (d *desktopDriver) Write(buf []byte) (int, error) {
	return 0, nil
}

func (d *desktopDriver) GetServerIP() net.IP {
	return nil
}
