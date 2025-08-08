package outlinebridge

import (
	"go_client/cloak"
)

func StartCloakClient(localHostC  *byte, localPortC  *byte, configC  *byte, udp bool) {
	cloak.StartCloakClient(localHost, localPort, config, udp)
}

func StopCloakClient() {
	cloak.StopCloakClient()
}
