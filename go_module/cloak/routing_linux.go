//go:build linux && !(android || ios)
// +build linux,!android,!ios

package cloak

import (
	"fmt"

	"go_module/routing"
	"go_module/tunnel/protected_dialer"

	"go_module/log"

	"github.com/jackpal/gateway"
)

func StartRoutingCloak(proxyIP string) error {
	if gateway, iface, ok := protected_dialer.GetDefaultRoute(); ok {
		if _, err := routing.EnsureProxyRoute(proxyIP, gateway, iface); err != nil {
			return fmt.Errorf("failed to add Cloak route for %s via protected route %s/%s: %w", log.MaskStr(proxyIP), gateway, iface, err)
		}
		log.Debugf(Category, "cloak client: installed protected direct route for %s via %s dev %s",
			log.MaskStr(proxyIP), gateway, iface)
		return nil
	}

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

	log.Debugf(Category, "cloak client: installed direct route for %s via %s dev %s",
		log.MaskStr(proxyIP), gateway, iface)
	return nil
}

func StopRoutingCloak(proxyIP string) {
	if gateway, iface, ok := protected_dialer.GetDefaultRoute(); ok {
		if err := routing.DeleteProxyRoute(proxyIP, gateway, iface); err != nil {
			log.Debugf(Category, "failed to remove protected specific route for %s via %s dev %s: %v",
				log.MaskStr(proxyIP), gateway, iface, err)
			return
		}
		log.Debugf(Category, "cloak client: removed protected direct route for %s via %s dev %s",
			log.MaskStr(proxyIP), gateway, iface)
		return
	}

	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		log.Debugf(Category, "failed to discover gateway while removing specific route for %s: %v",
			log.MaskStr(proxyIP), err)
		return
	}
	gateway := gatewayIP.String()

	iface, err := routing.GetDefaultInterfaceNameLinux(gateway)
	if err != nil {
		log.Debugf(Category, "failed to find uplink interface while removing specific route for %s via %s: %v",
			log.MaskStr(proxyIP), gateway, err)
		return
	}

	if err := routing.DeleteProxyRoute(proxyIP, gateway, iface); err != nil {
		log.Debugf(Category, "failed to remove specific route for %s via %s dev %s: %v",
			log.MaskStr(proxyIP), gateway, iface, err)
		return
	}

	log.Debugf(Category, "cloak client: removed direct route for %s via %s dev %s",
		log.MaskStr(proxyIP), gateway, iface)
}
