//go:build !(android || ios)

package api

import (
	apiCommon "go_module/desktop_exports/common"
	"go_module/log"
	"go_module/vpnmanager"
)

func StartCloakClient(localHost, localPort, config string, udp bool) error {
	log.Debugf(apiCommon.Category, "StartCloakClient")
	if err := vpnmanager.StartCloakClient(apiCommon.Category, localHost, localPort, config, udp); err != nil {
		log.Debugf(apiCommon.Category, "StartCloakClient failed: %v", err)
		return err
	}
	log.Debugf(apiCommon.Category, "end StartCloakClient")
	return nil
}

func StopCloakClient() {
	vpnmanager.StopCloakClient(apiCommon.Category)
}
