package main

import "C"
import (
	"go_client/cloak"
)

//export StartCloakClient
func StartCloakClient(localHost *C.char, localPort *C.char, config *C.char, udp bool) {
	cloak.StartCloakClient(
		C.GoString(localHost),
		C.GoString(localPort),
		C.GoString(config),
		bool(udp),
	)
}

//export StopCloakClient
func StopCloakClient() {
	cloak.StopCloakClient()
}
