//go:build !(android || ios)

package api

import (
	"go_module/cloak"
	"go_module/log"
)

func StartCloakClient(localHost, localPort, config string, udp bool) error {
	log.Infof("StartCloakClient")
	if err := cloak.StartCloakClient(
		localHost,
		localPort,
		config,
		bool(udp),
	); err != nil {
		log.Infof("StartCloakClient failed: %v", err)
		return err
	}
	log.Infof("end StartCloakClient")
	return nil
}

func StopCloakClient() {
	cloak.StopCloakClient()
}
