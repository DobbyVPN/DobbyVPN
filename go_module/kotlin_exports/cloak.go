//go:build android

package main

import (
	"C"
	"go_module/cloak"
)
import "strings"

//export StartCloakClient
func StartCloakClient(localHost string, localPort string, config string, udp bool) {
	cloak.StartCloakClient(strings.Clone(localHost), strings.Clone(localPort), strings.Clone(config), udp)
}

//export StopCloakClient
func StopCloakClient() {
	cloak.StopCloakClient()
}
