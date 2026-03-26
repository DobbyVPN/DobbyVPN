//go:build darwin && !(android || ios)
// +build darwin,!android,!ios

package internal

import (
	"fmt"

	"go_client/routing"
	xrayCommon "go_client/xray/common"
)

type macosConfigurator struct{}

func newPlatformConfigurator() PlatformConfigurator {
	return &macosConfigurator{}
}

func (c *macosConfigurator) TunDeviceName() string {
	return "utun66"
}

func (c *macosConfigurator) SetupInterfaceAndRouting(serverIP, physGateway string) error {
	if _, err := routing.ExecuteCommand(fmt.Sprintf("sudo ifconfig %s %s %s up", c.TunDeviceName(), xrayCommon.TunIP, xrayCommon.TunGateway)); err != nil {
		return fmt.Errorf("failed to configure tun ip: %w", err)
	}
	if err := routing.StartRouting(serverIP, physGateway, c.TunDeviceName()); err != nil {
		return fmt.Errorf("routing setup failed: %w", err)
	}
	return nil
}

func (c *macosConfigurator) TeardownRouting(serverIP, physGateway string) {
	routing.StopRouting(serverIP, physGateway)
}
