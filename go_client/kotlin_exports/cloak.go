package main

import (
    "C"
	"go_client/cloak"
)

//export StartCloakClient
func StartCloakClient(localHostC  *C.char, localPortC  *C.char, configC  *C.char, udp bool) {
	localHost := C.GoString(localHostC)
	localPort := C.GoString(localPortC)
	config := C.GoString(configC)
	cloak.StartCloakClient(localHost, localPort, config, udp)
}

//export StopCloakClient
func StopCloakClient() {
	cloak.StopCloakClient()
}
