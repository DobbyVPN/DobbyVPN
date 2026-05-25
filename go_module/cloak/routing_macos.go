//go:build darwin && !(android || ios)
// +build darwin,!android,!ios

package cloak

import (
	"fmt"

	"go_module/routing"

	"github.com/jackpal/gateway"
	"go_module/log"
)

func StartRoutingCloak(proxyIP string) error {
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		return fmt.Errorf("failed to discover gateway for Cloak route: %w", err)
	}
	addSpecificRoute2 := fmt.Sprintf("sudo route add -net %s/32 %s", proxyIP, gatewayIP.String())

	if _, err := routing.ExecuteCommand(addSpecificRoute2); err != nil {
		log.Infof("failed to add specific route: %v", err)
		return fmt.Errorf("failed to add Cloak route for %s via %s: %w", proxyIP, gatewayIP.String(), err)
	}

	return nil
}

func StopRoutingCloak(proxyIP string) error {
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		return fmt.Errorf("failed to discover gateway for Cloak route removal: %w", err)
	}
	removeSpecificRoute := fmt.Sprintf("sudo route delete -net %s/32 %s", proxyIP, gatewayIP.String())
	if _, err := routing.ExecuteCommand(removeSpecificRoute); err != nil {
		log.Infof("failed to remove specific route: %v", err)
		return fmt.Errorf("failed to remove Cloak route for %s via %s: %w", proxyIP, gatewayIP.String(), err)
	}

	return nil
}
