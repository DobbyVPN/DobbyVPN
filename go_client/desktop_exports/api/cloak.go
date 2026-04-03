package api

import (
	"go_client/cloak"
	"go_client/log"
)

func StartCloakClient(localHost, localPort, config string, udp bool) {
	log.Infof("StartCloakClient")
	cloak.StartCloakClient(
		localHost,
		localPort,
		config,
		bool(udp),
	)
	log.Infof("end StartCloakClient")
}

func StopCloakClient() {
	cloak.StopCloakClient()
}
