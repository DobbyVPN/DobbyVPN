//go:build linux && !(android || ios)
// +build linux,!android,!ios

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
		return fmt.Errorf("failed to discover gateway: %w", err)
	}
	gateway := gatewayIP.String()

	iface, err := routing.GetDefaultInterfaceNameLinux(gateway)
	if err != nil {
		return fmt.Errorf("failed to find uplink interface for gateway %s: %w", gateway, err)
	}

	if _, err := routing.EnsureProxyRoute(proxyIP, gateway, iface); err != nil {
		return fmt.Errorf("failed to add specific route for %s via %s dev %s: %w", proxyIP, gateway, iface, err)
	}

	log.Infof("cloak client: installed direct route for %s via %s dev %s",
		log.MaskStr(proxyIP), gateway, iface)
	return nil
}

func StopRoutingCloak(proxyIP string) {
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		log.Infof("failed to discover gateway while removing specific route for %s: %v",
			log.MaskStr(proxyIP), err)
		return
	}
	gateway := gatewayIP.String()

	iface, err := routing.GetDefaultInterfaceNameLinux(gateway)
	if err != nil {
		log.Infof("failed to find uplink interface while removing specific route for %s via %s: %v",
			log.MaskStr(proxyIP), gateway, err)
		return
	}

	if err := routing.DeleteProxyRoute(proxyIP, gateway, iface); err != nil {
		log.Infof("failed to remove specific route for %s via %s dev %s: %v",
			log.MaskStr(proxyIP), gateway, iface, err)
		return
	}

	log.Infof("cloak client: removed direct route for %s via %s dev %s",
		log.MaskStr(proxyIP), gateway, iface)
}
