package outlinebridge

import (
	"go_client/cloak"
)

func StartCloakClient(localHost  *byte, localPort  *byte, config  *byte, udp bool) {
	cloak.StartCloakClient(localHost, localPort, config, udp)
}

func StopCloakClient() {
	cloak.StopCloakClient()
}
