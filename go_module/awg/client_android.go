//go:build android

package awg

import (
	"errors"
	"fmt"
	"go_module/awg/internal"
	"go_module/common"
	_ "go_module/log"
)

const Name = "awg"

type AwgClient struct {
	App *internal.App
}

func (a *AwgClient) Connect() error {
	if a == nil || a.App == nil {
		return errors.New("awg android client is not initialized")
	}
	common.Client.MarkActive(Name)
	if err := a.App.Run(); err != nil {
		return fmt.Errorf("failed to run awg android app: %w", err)
	}
	return nil
}

func (a *AwgClient) Disconnect() error {
	if a == nil || a.App == nil {
		return errors.New("awg android client is not initialized")
	}
	common.Client.MarkInactive(Name)
	a.App.Stop()
	return nil
}

func (a *AwgClient) Refresh() error {
	if a == nil || a.App == nil {
		return errors.New("awg android client is not initialized")
	}
	a.App.Stop()
	if err := a.App.Run(); err != nil {
		return fmt.Errorf("failed to refresh awg android app: %w", err)
	}
	return nil
}

func (c *AwgClient) HealthCheck() error {
	return nil
}

func NewAwgClient(interfaceName, interfaceConfig string, interfaceFd int) (*AwgClient, error) {
	app, err := internal.NewApp(interfaceName, interfaceConfig, interfaceFd)
	if err != nil {
		return nil, fmt.Errorf("failed to create awg android app: %w", err)
	}

	cl := &AwgClient{App: app}
	common.Client.SetVpnClient(Name, cl)
	return cl, nil
}
