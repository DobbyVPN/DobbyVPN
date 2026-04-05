package internal

import (
	"fmt"
	"time"

	"go_client/awg/config"
	"go_client/awg/subnet"
	"go_client/awg/tunnel"
	"go_client/log"
)

type App struct {
	TunnelData *tunnel.TunnelData
}

// NewApp creates a new App using a tunnel name and its config
func NewApp(tun, conf string) (*App, error) {
	awgqconfig, err := config.FromWgQuickWithUnknownEncoding(conf, tun)
	if err != nil {
		return nil, fmt.Errorf("Failed to read awg-quick config: %s", err)
	}

	tunnelData := tunnel.CreateTunnelData(tun, awgqconfig)
	app := &App{
		TunnelData: tunnelData,
	}

	return app, nil
}

func (a *App) Run() error {
	err := a.TunnelData.Run()
	if err != nil {
		return fmt.Errorf("Failed to run runnel: %s", err)
	}

	log.Infof("Wait for tunnel to run")
	time.Sleep(100 * time.Millisecond)

	subnetData := subnet.CreateSubnetData(a.TunnelData.InterfaceName, a.TunnelData.InterfaceConfig, a.TunnelData.TunnelDevice, a.TunnelData.TunnelBind)
	err = subnetData.ConfigureSubnet()
	if err != nil {
		a.TunnelData.Stop()

		return fmt.Errorf("Failed to configure subnet: %s", err)
	}

	return nil
}

func (a *App) Stop() {
	a.TunnelData.Stop()
}
