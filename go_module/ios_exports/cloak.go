//go:build ios

package cloak_outline

import (
	"fmt"

	"go_module/vpnmanager"
)

func StartCloakClient(localHost string, localPort string, config string, udp bool) (err error) {
	defer guardErr("StartCloakClient", &err)()
	if err := vpnmanager.StartCloakClient(logCategory, localHost, localPort, config, udp); err != nil {
		return fmt.Errorf("StartCloakClient failed: %w", err)
	}
	return nil
}

func StopCloakClient() {
	defer guard("StopCloakClient")()
	vpnmanager.StopCloakClient(logCategory)
}
