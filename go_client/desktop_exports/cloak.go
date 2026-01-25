package main

import "C"
import (
	"go_client/cloak"
	log "go_client/logger"
)

//export StartCloakClient
func StartCloakClient(localHost *C.char, localPort *C.char, config *C.char, udp bool) {
	log.Infof("StartCloakClient")
	cloak.StartCloakClient(
		C.GoString(localHost),
		C.GoString(localPort),
		C.GoString(config),
		bool(udp),
	)
	log.Infof("end StartCloakClient")
}

//export StopCloakClient
func StopCloakClient() {
	cloak.StopCloakClient()
}
