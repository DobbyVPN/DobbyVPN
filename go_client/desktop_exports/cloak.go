package main

import "C"
import (
	"go_client/cloak"

	log "github.com/sirupsen/logrus"
)

//export StartCloakClient
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

//export StopCloakClient
func StopCloakClient() {
	cloak.StopCloakClient()
}
