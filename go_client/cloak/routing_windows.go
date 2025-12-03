//go:build windows && !(android || ios)
// +build windows,!android,!ios

package cloak

import (
	"fmt"

	"go_client/routing"

	"github.com/jackpal/gateway"
	log "github.com/sirupsen/logrus"
)

func StartRoutingCloak(proxyIP string) error {
	log.Infof("StartRoutingCloak(%s)\n", proxyIP)
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		log.Infof("Can't find gatewayIP, err = %v \n", err)
		return err
	}
	log.Infof("found gatewayIP = %s\n", gatewayIP.String())
	interfaceName, err := routing.FindInterfaceByGateway(gatewayIP.String())
	if err != nil {
		log.Infof("Can't find interfaceName, err = %v \n", err)
		return err
	}
	log.Infof("found interfaceName = %s\n", interfaceName)

	netInterface, err := routing.GetNetworkInterfaceByIP(interfaceName)
	command := fmt.Sprintf("route change %s %s if \"%s\"", proxyIP, gatewayIP.String(), netInterface.Name)
	_, err = routing.ExecuteCommand(command)
	if err != nil {
		netshCommand := fmt.Sprintf("netsh interface ipv4 add route %s/32 nexthop=%s interface=\"%s\" metric=0 store=active",
			proxyIP, gatewayIP.String(), netInterface.Name)
		_, err = routing.ExecuteCommand(netshCommand)
		if err != nil {
			log.Infof("Outline/routing: Failed to add or update proxy route for IP %s: %v\n", proxyIP, err)
		}
	}
	return nil
}

func StopRoutingCloak(proxyIp string) {
	log.Infof("Outline/routing: Cleaning up routing table and rules...")
	command := fmt.Sprintf("route delete %s", proxyIp)
	_, err := routing.ExecuteCommand(command)
	if err != nil {
		log.Infof("Outline/routing: Failed to delete proxy route for IP %s: %v\n", proxyIp, err)
	}
	log.Infof("Outline/routing: Cleaned up routing table and rules.")
}
