//go:build darwin
// +build darwin

package cloak

import (
	"fmt"

	"go_client/routing"

	"github.com/jackpal/gateway"
	log "github.com/sirupsen/logrus"
)

func StartRoutingCloak(proxyIP string) error {
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		panic(err)
	}
	addSpecificRoute2 := fmt.Sprintf("sudo route add -net %s/32 %s", proxyIP, gatewayIP.String())

	if _, err := routing.ExecuteCommand(addSpecificRoute2); err != nil {
		log.Infof("failed to add specific route: %w", err)
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
		log.Infof("failed to remove specific route: %w", err)
	}

	return nil
}
