//go:build !(android || ios)

package api

import (
	"go_module/cloak"
	"go_module/log"
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
