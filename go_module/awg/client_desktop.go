//go:build !(android || ios)

package awg

import (
	"errors"
	"fmt"
	"go_module/awg/internal"
	"go_module/common"
)

const Name = "awg"

type AwgClient struct {
	app *internal.App
}

func (a *AwgClient) Connect() error {
	if a == nil || a.app == nil {
		return errors.New("awg desktop client is not initialized")
	}
	common.Client.MarkActive(Name)
	if err := a.app.Run(); err != nil {
		return fmt.Errorf("failed to run awg desktop app: %w", err)
	}
	return nil
}

func (a *AwgClient) Disconnect() error {
	if a == nil || a.app == nil {
		return errors.New("awg desktop client is not initialized")
	}
	common.Client.MarkInactive(Name)
	a.app.Stop()
	return nil
}

func (a *AwgClient) HealthCheck() error {
	return nil
}

func (a *AwgClient) Refresh() error {
	if a == nil || a.app == nil {
		return errors.New("awg desktop client is not initialized")
	}
	a.app.Stop()
	if err := a.app.Run(); err != nil {
		return fmt.Errorf("failed to refresh awg desktop app: %w", err)
	}
	return nil
}

func NewAwgClient(config string) (*AwgClient, error) {
	app, err := internal.NewApp("awg0", config)
	if err != nil {
		return nil, fmt.Errorf("failed to create awg desktop app: %w", err)
	}

	cl := &AwgClient{app: app}
	common.Client.SetVpnClient(Name, cl)
	return cl, nil
}
