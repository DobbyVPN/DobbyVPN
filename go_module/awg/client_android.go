//go:build android

package awg

import (
	"go_module/awg/internal"
	"go_module/common"
	_ "go_module/log"
)

const Name = "awg"

type AwgClient struct {
	App *internal.App
}

func (a *AwgClient) Connect() error {
	common.Client.MarkActive(Name)
	return a.App.Run()
}

func (a *AwgClient) Disconnect() error {
	common.Client.MarkInactive(Name)
	a.App.Stop()
	return nil
}

func (a *AwgClient) Refresh() error {
	a.App.Stop()
	return a.App.Run()
}

func NewAwgClient(interfaceName, interfaceConfig string, interfaceFd int32) (*AwgClient, error) {
	app, err := internal.NewApp(interfaceName, interfaceConfig, int(interfaceFd))
	if err != nil {
		return nil, err
	}

	cl := &AwgClient{App: app}
	common.Client.SetVpnClient(Name, cl)
	return cl, nil
}
