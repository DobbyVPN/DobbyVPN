//go:build android

package main

import (
	"C"
	"go_module/cloak"
)

//export StartCloakClient
func StartCloakClient(localHost string, localPort string, config string, udp bool) {
	cloak.StartCloakClient(localHost, localPort, config, udp)
}

//export StopCloakClient
func StopCloakClient() {
	cloak.StopCloakClient()
}
