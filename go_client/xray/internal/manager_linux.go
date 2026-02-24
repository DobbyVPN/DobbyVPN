//go:build linux && !android
// +build linux,!android

package internal

import (
	"fmt"
	"go_client/routing"
	xrayCommon "go_client/xray/common"
)

type linuxConfigurator struct{}

func newPlatformConfigurator() PlatformConfigurator {
	return &linuxConfigurator{}
}

func (c *linuxConfigurator) TunDeviceName() string {
	return "tun0"
}

func (c *linuxConfigurator) SetupInterfaceAndRouting(serverIP, physGateway string) error {
	if _, err := routing.ExecuteCommand(fmt.Sprintf("sudo ip addr add %s dev %s", xrayCommon.TunCIDR, c.TunDeviceName())); err != nil {
		return fmt.Errorf("failed to set tun ip: %w", err)
	}
	if _, err := routing.ExecuteCommand(fmt.Sprintf("sudo ip link set dev %s up", c.TunDeviceName())); err != nil {
		return fmt.Errorf("failed to up tun: %w", err)
	}
	if err := routing.StartRouting(serverIP, physGateway, c.TunDeviceName()); err != nil {
		return fmt.Errorf("routing failed: %w", err)
	}
	return nil
}

func (c *linuxConfigurator) TeardownRouting(serverIP, physGateway string) {
	routing.StopRouting(serverIP, physGateway)
}
