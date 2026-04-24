//go:build windows && !(android || ios)
// +build windows,!android,!ios

package cloak

import (
	"fmt"

	"go_module/routing"

	"go_module/log"

	"github.com/jackpal/gateway"
)

func StartRoutingCloak(proxyIP string) error {
	log.SimpleDebugf(Category, "StartRoutingCloak(%s)\n", log.MaskStr(proxyIP))
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		log.SimpleErrorf(Category, "Can't find gatewayIP, err = %v \n", err)
		return err
	}
	log.SimpleDebugf(Category, "found gatewayIP = %s\n", gatewayIP.String())
	interfaceName, err := routing.FindInterfaceIPByGateway(gatewayIP.String())
	if err != nil {
		log.SimpleErrorf(Category, "Can't find interfaceName, err = %v \n", err)
		return err
	}
	log.SimpleDebugf(Category, "found interfaceName = %s\n", interfaceName)

	netInterface, err := routing.GetNetworkInterfaceByIP(interfaceName)
	command := fmt.Sprintf("route change %s %s if \"%s\"", proxyIP, gatewayIP.String(), netInterface.Name)
	_, err = routing.ExecuteCommand(command)
	if err != nil {
		netshCommand := fmt.Sprintf("netsh interface ipv4 add route %s/32 nexthop=%s interface=\"%s\" metric=0 store=active",
			proxyIP, gatewayIP.String(), netInterface.Name)
		_, err = routing.ExecuteCommand(netshCommand)
		if err != nil {
			log.SimpleWarnf(Category, "Cloak/routing: Failed to add or update proxy route for IP %s: %v", log.MaskStr(proxyIP), err)
		}
	}
	return nil
}

func StopRoutingCloak(proxyIp string) {
	log.SimpleDebugf(Category, "Cloak/routing: Cleaning up routing table and rules...")
	command := fmt.Sprintf("route delete %s", proxyIp)
	_, err := routing.ExecuteCommand(command)
	if err != nil {
		log.SimpleWarnf(Category, "Cloak/routing: Failed to delete proxy route for IP %s: %v\n", log.MaskStr(proxyIp), err)
	}
	log.SimpleInfof(Category, "Cloak/routing: Cleaned up routing table and rules.")
}
