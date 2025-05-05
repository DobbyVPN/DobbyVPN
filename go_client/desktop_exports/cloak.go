package main

import (
	"go_client/cloak"
)

func StartCloakClient(localHost, localPort, config string, udp bool) {
	cloak.StartCloakClient(localHost, localPort, config, udp)
}

func StopCloakClient() {
	cloak.StopCloakClient()
}
