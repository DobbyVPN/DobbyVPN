//go:build windows

package internal

import (
	"fmt"
	log "go_client/logger"
	"go_client/routing"
	xrayCommon "go_client/xray/common"
)

type windowsConfigurator struct {
	interfaceName string
}

func newPlatformConfigurator() PlatformConfigurator {
	return &windowsConfigurator{}
}

func (c *windowsConfigurator) TunDeviceName() string {
	return "wintun"
}

func (c *windowsConfigurator) SetupInterfaceAndRouting(serverIP, physGateway string) error {
	physInterface, err := routing.FindInterfaceByGateway(physGateway)
	if err != nil {
		return fmt.Errorf("failed to find network interface by gateway %s: %w", physGateway, err)
	}

	netInterface, err := routing.GetNetworkInterfaceByIP(physInterface)
	if err != nil {
		return fmt.Errorf("failed to resolve network interface for %s: %w", physInterface, err)
	}
	c.interfaceName = netInterface.Name

	if _, err = routing.ExecuteCommand(fmt.Sprintf("netsh interface ip set address \"%s\" static %s %s %s", c.TunDeviceName(), xrayCommon.TunIP, xrayCommon.TunMask, xrayCommon.TunGateway)); err != nil {
		return fmt.Errorf("failed to set TUN address: %w", err)
	}
	if _, err = routing.ExecuteCommand(fmt.Sprintf("netsh interface ip set dns \"%s\" static 8.8.8.8", c.TunDeviceName())); err != nil {
		return fmt.Errorf("failed to set TUN DNS: %w", err)
	}

	dst := netInterface.HardwareAddr
	var spoofedMac []byte
	if len(dst) == 0 {
		spoofedMac = []byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}
		log.Infof("[Routing] Wintun has no MAC, using dummy: %v", spoofedMac)
	} else {
		spoofedMac = make([]byte, len(dst))
		copy(spoofedMac, dst)
		if len(spoofedMac) > 2 {
			spoofedMac[2] += 2
		}
	}

	err = routing.StartRouting(
		serverIP,
		physGateway,
		c.TunDeviceName(),
		dst.String(),
		c.interfaceName,
		xrayCommon.TunGateway,
		xrayCommon.TunIP,
		spoofedMac,
	)

	return err
}

func (c *windowsConfigurator) TeardownRouting(serverIP, physGateway string) {
	routing.StopRouting(serverIP, c.TunDeviceName(), physGateway, c.interfaceName, xrayCommon.TunGateway)
}
