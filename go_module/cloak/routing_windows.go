//go:build windows && !(android || ios)
// +build windows,!android,!ios

package cloak

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go_module/routing"
	"go_module/tunnel/protected_dialer"

	"go_module/log"

	"github.com/jackpal/gateway"
)

var (
	cloakRoutingMu       sync.Mutex
	cloakRoutingPlan     *routing.Plan
	cloakRoutingSequence atomic.Uint64
)

func StartRoutingCloak(proxyIP string) error {
	log.Debugf(Category, "StartRoutingCloak(%s)\n", log.MaskStr(proxyIP))
	cloakRoutingMu.Lock()
	defer cloakRoutingMu.Unlock()
	if cloakRoutingPlan != nil {
		return fmt.Errorf("Cloak routing lease is already active")
	}

	gatewayIP, interfaceName, ok := protected_dialer.GetDefaultRoute()
	if !ok {
		discoveredGateway, err := gateway.DiscoverGateway()
		if err != nil {
			log.Debugf(Category, "Can't find gatewayIP, err = %v \n", err)
			return err
		}
		gatewayIP = discoveredGateway.String()
		interfaceIP, err := routing.FindInterfaceIPByGateway(gatewayIP)
		if err != nil {
			log.Debugf(Category, "Can't find interfaceName, err = %v \n", err)
			return err
		}
		netInterface, err := routing.GetNetworkInterfaceByIP(interfaceIP)
		if err != nil {
			return fmt.Errorf("resolve Cloak uplink interface: %w", err)
		}
		interfaceName = netInterface.Name
	}
	log.Debugf(Category, "Cloak/routing: using protected default route gateway=%s interface=%s", gatewayIP, interfaceName)

	plan := routing.NewPlan(fmt.Sprintf(
		"windows-cloak-%d-%d",
		time.Now().UnixNano(),
		cloakRoutingSequence.Add(1),
	))
	if _, err := routing.AcquireProxyRoute(plan, proxyIP, gatewayIP, interfaceName); err != nil {
		_ = plan.Close()
		return fmt.Errorf(
			"failed to acquire Cloak route for %s via protected route: %w",
			log.MaskStr(proxyIP),
			err,
		)
	}
	cloakRoutingPlan = plan
	return nil
}

func StopRoutingCloak(_ string) {
	cloakRoutingMu.Lock()
	plan := cloakRoutingPlan
	cloakRoutingPlan = nil
	cloakRoutingMu.Unlock()
	if plan == nil {
		return
	}
	if err := plan.Close(); err != nil {
		log.Debugf(Category, "Cloak/routing: owned route cleanup failed: %v", err)
	}
}
