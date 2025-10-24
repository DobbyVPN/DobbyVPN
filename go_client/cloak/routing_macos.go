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
		return fmt.Errorf("failed to discover gateway: %w", err)
	}

	commands := []string{
		fmt.Sprintf("route add -net %s/32 %s", proxyIP, gatewayIP.String()),
	}

	if _, err := routing.ExecuteAsAdmin(commands); err != nil {
		log.Warnf("failed to add specific route: %v", err)
		return err
	}

	log.Infof("Successfully added specific route to %s via %s", proxyIP, gatewayIP)
	return nil
}

func StopRoutingCloak(proxyIP string) error {
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		return fmt.Errorf("failed to discover gateway: %w", err)
	}

	commands := []string{
		fmt.Sprintf("route delete -net %s/32 %s", proxyIP, gatewayIP.String()),
	}

	if _, err := routing.ExecuteAsAdmin(commands); err != nil {
		log.Warnf("failed to remove specific route: %v", err)
		return err
	}

	log.Infof("Successfully removed specific route to %s", proxyIP)
	return nil
}
