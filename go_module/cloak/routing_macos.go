//go:build darwin && !(android || ios)
// +build darwin,!android,!ios

package cloak

import (
	"fmt"

	"go_module/routing"

	"go_module/log"

	"github.com/jackpal/gateway"
)

func StartRoutingCloak(proxyIP string) error {
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		return fmt.Errorf("failed to discover gateway for Cloak route: %w", err)
	}
	addSpecificRoute2 := fmt.Sprintf("route -n add -host %s %s", proxyIP, gatewayIP.String())

	if _, err := routing.ExecuteCommand(addSpecificRoute2); err != nil {
		log.Debugf(Category, "failed to add specific route: %v", err)
		return fmt.Errorf("failed to add Cloak route for %s via %s: %w", proxyIP, gatewayIP.String(), err)
	}

	return nil
}

func StopRoutingCloak(proxyIP string) error {
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		return fmt.Errorf("failed to discover gateway for Cloak route removal: %w", err)
	}
	removeSpecificRoute := fmt.Sprintf("route -n delete -host %s %s", proxyIP, gatewayIP.String())
	if _, err := routing.ExecuteCommand(removeSpecificRoute); err != nil {
		log.Debugf(Category, "failed to remove specific route: %v", err)
		return fmt.Errorf("failed to remove Cloak route for %s via %s: %w", proxyIP, gatewayIP.String(), err)
	}

	return nil
}
