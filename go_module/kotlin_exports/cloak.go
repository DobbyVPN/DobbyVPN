//go:build android

package dobbyvpn

import (
	"go_module/log"
	"go_module/vpnmanager"
	"strings"
)

func StartCloakClient(localHost string, localPort string, config string, udp bool) (result int32) {
	defer vpnmanager.GuardExport(logCategory, "StartCloakClient", lastError)()
	lastError.Clear()
	result = -1

	if err := vpnmanager.StartCloakClient(
		logCategory,
		strings.Clone(localHost),
		strings.Clone(localPort),
		strings.Clone(config),
		udp,
	); err != nil {
		lastError.Set(err.Error())
		log.Debugf(logCategory, "StartCloakClient failed: %v", err)
		return result
	}
	return 0
}

func StopCloakClient() {
	vpnmanager.StopCloakClient(logCategory)
}
