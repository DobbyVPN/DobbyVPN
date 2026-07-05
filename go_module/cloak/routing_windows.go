//go:build windows && !(android || ios)
// +build windows,!android,!ios

package cloak

import (
	"fmt"

	"go_module/routing"
	"go_module/tunnel/protected_dialer"

	"go_module/log"

	"github.com/jackpal/gateway"
)

func StartRoutingCloak(proxyIP string) error {
	log.Debugf(Category, "StartRoutingCloak(%s)\n", log.MaskStr(proxyIP))
	if gatewayIP, interfaceName, ok := protected_dialer.GetDefaultRoute(); ok {
		log.Debugf(Category, "Cloak/routing: using protected default route gateway=%s interface=%s", gatewayIP, interfaceName)
		if _, err := routing.EnsureProxyRoute(proxyIP, gatewayIP, interfaceName); err != nil {
			return fmt.Errorf("failed to add Cloak route for %s via protected route %s/%s: %w", log.MaskStr(proxyIP), gatewayIP, interfaceName, err)
		}
		return nil
	}

	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		log.Debugf(Category, "Can't find gatewayIP, err = %v \n", err)
		return err
	}
	log.Debugf(Category, "found gatewayIP = %s\n", gatewayIP.String())
	interfaceName, err := routing.FindInterfaceIPByGateway(gatewayIP.String())
	if err != nil {
		log.Debugf(Category, "Can't find interfaceName, err = %v \n", err)
		return err
	}
	log.Debugf(Category, "found interfaceName = %s\n", interfaceName)

	netInterface, err := routing.GetNetworkInterfaceByIP(interfaceName)
	command := fmt.Sprintf("route change %s %s if \"%s\"", proxyIP, gatewayIP.String(), netInterface.Name)
	_, err = routing.ExecuteCommand(command)
	if err != nil {
		netshCommand := fmt.Sprintf("netsh interface ipv4 add route %s/32 nexthop=%s interface=\"%s\" metric=0 store=active",
			proxyIP, gatewayIP.String(), netInterface.Name)
		_, err = routing.ExecuteCommand(netshCommand)
		if err != nil {
			log.Debugf(Category, "Cloak/routing: Failed to add or update proxy route for IP %s: %v", log.MaskStr(proxyIP), err)
		}
	}
	return nil
}

func StopRoutingCloak(proxyIp string) {
	log.Debugf(Category, "Cloak/routing: Cleaning up routing table and rules...")
	if gatewayIP, interfaceName, ok := protected_dialer.GetDefaultRoute(); ok {
		if err := routing.DeleteProxyRoute(proxyIp, gatewayIP, interfaceName); err != nil {
			log.Debugf(Category, "Cloak/routing: Failed to delete protected proxy route for IP %s via %s/%s: %v\n", log.MaskStr(proxyIp), gatewayIP, interfaceName, err)
		}
		log.Debugf(Category, "Cloak/routing: Cleaned up protected route.")
		return
	}

	command := fmt.Sprintf("route delete %s", proxyIp)
	_, err := routing.ExecuteCommand(command)
	if err != nil {
		log.Debugf(Category, "Cloak/routing: Failed to delete proxy route for IP %s: %v\n", log.MaskStr(proxyIp), err)
	}
	log.Debugf(Category, "Cloak/routing: Cleaned up routing table and rules.")
}
