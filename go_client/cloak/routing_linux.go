//go:build linux && !(android || ios)
// +build linux,!android,!ios

package cloak

import (
	"fmt"

	"go_client/routing"

	"github.com/jackpal/gateway"
	log "go_client/logger"
)

func StartRoutingCloak(proxyIP string) error {
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		return fmt.Errorf("failed to discover gateway: %w", err)
	}
	if _, err := routing.ExecuteCommand(fmt.Sprintf("sudo ip route add %s/32 via %s", proxyIP, gatewayIP.String())); err != nil {
		log.Infof("failed to add specific route for %s: %v", log.MaskStr(proxyIP), err)
	}

	return nil
}

func StopRoutingCloak(proxyIP string) {
	if _, err := routing.ExecuteCommand(fmt.Sprintf("sudo ip route del %s/32", proxyIP)); err != nil {
		log.Infof("failed to remove specific route for %s: %v", log.MaskStr(proxyIP), err)
	}
}
