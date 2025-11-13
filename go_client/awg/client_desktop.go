//go:build !(android || ios)

package awg

import (
	"go_client/awg/internal"
	"go_client/common"
)

const Name = "awg"

type AwgClient struct {
	app *internal.App
}

func (a *AwgClient) Connect() error {
	common.Client.MarkActive(Name)
	return a.app.Run()
}

func (a *AwgClient) Disconnect() error {
	common.Client.MarkInactive(Name)
	a.app.Stop()
	return nil
}

func (a *AwgClient) Refresh() error {
	a.app.Stop()
	return a.app.Run()
}

func NewAwgClient(interface_name, awgq_config string) (*AwgClient, error) {
	app, err := internal.NewApp(interface_name, awgq_config)
	if err != nil {
		return nil, err
	}

	cl := &AwgClient{app: app}
	common.Client.SetVpnClient(Name, cl)
	return cl, nil
}
