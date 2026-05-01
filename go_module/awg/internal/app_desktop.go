//go:build !android

package internal

import (
	"fmt"

	"go_module/awg/config"
	"go_module/awg/tunnel"
)

type App struct {
	TunnelData *tunnel.TunnelData
}

// NewApp creates a new App using a tunnel name and its config
func NewApp(tun, conf string) (*App, error) {
	awgqconfig, err := config.FromWgQuickWithUnknownEncoding(conf, tun)
	if err != nil {
		return nil, fmt.Errorf("failed to read awg-quick config: %w", err)
	}

	tunnelData := &tunnel.TunnelData{
		InterfaceName:   tun,
		InterfaceConfig: awgqconfig,
	}
	app := &App{
		TunnelData: tunnelData,
	}

	return app, nil
}

func (a *App) Run() error {
	err := a.TunnelData.Run()
	if err != nil {
		return fmt.Errorf("failed to run runnel: %w", err)
	}

	return nil
}

func (a *App) Stop() {
	a.TunnelData.Stop()
}
