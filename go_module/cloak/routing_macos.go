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
		panic(err)
	}
	addSpecificRoute2 := fmt.Sprintf("sudo route add -net %s/32 %s", proxyIP, gatewayIP.String())

	if _, err := routing.ExecuteCommand(addSpecificRoute2); err != nil {
		log.Warnf(Category, "failed to add specific route: %v", err)
	}

	return nil
}

func StopRoutingCloak(proxyIP string) error {
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		panic(err)
	}
	removeSpecificRoute := fmt.Sprintf("sudo route delete -net %s/32 %s", proxyIP, gatewayIP.String())
	if _, err := routing.ExecuteCommand(removeSpecificRoute); err != nil {
		log.Warnf(Category, "failed to remove specific route: %v", err)
	}

	return nil
}
