//go:build android

package main

import (
	"C"
	"go_module/cloak"
)

//export StartCloakClient
func StartCloakClient(localHost string, localPort string, config string, udp int32) {
	cloak.StartCloakClient(localHost, localPort, config, udp != 0)
}

//export StopCloakClient
func StopCloakClient() {
	cloak.StopCloakClient()
}
