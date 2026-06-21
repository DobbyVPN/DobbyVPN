//go:build !(android || ios)

package api

import (
	"go_module/cloak"
	"go_module/desktop_exports/common"
	"go_module/log"
)

func StartCloakClient(localHost, localPort, config string, udp bool) error {
	log.Debugf(common.Category, "StartCloakClient")
	if err := cloak.StartCloakClient(
		localHost,
		localPort,
		config,
		bool(udp),
	); err != nil {
		log.Debugf(common.Category, "StartCloakClient failed: %v", err)
		return err
	}
	log.Debugf(common.Category, "end StartCloakClient")
	return nil
}

func StopCloakClient() {
	cloak.StopCloakClient()
}
