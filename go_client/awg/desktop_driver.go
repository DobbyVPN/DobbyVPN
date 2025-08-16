//go:build !(android || ios)

package awg

import (
	"go_client/awg/internal"
	"go_client/common"
)

const Name = "awg"

type desktopDriver struct {
	app *internal.App
}

func (d *desktopDriver) Connect() error {
	common.Client.MarkActive(Name)
	return d.app.Run()
}

func (d *desktopDriver) Disconnect() error {
	common.Client.MarkInactive(Name)
	d.app.Stop()
	return nil
}

func (d *desktopDriver) Refresh() error {
	d.app.Stop()
	return d.app.Run()
}

func newDriver(config string) (Driver, error) {
	app, err := internal.NewApp(config)
	if err != nil {
		return nil, err
	}
	return &desktopDriver{app: app}, nil
}
