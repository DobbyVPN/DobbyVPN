package cloak_outline

import (
	"go_module/cloak"
)

func StartCloakClient(localHost string, localPort string, config string, udp bool) error {
	return cloak.StartCloakClient(localHost, localPort, config, udp)
}

func StopCloakClient() {
	cloak.StopCloakClient()
}
