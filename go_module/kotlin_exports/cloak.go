//go:build android

package dobbyvpn

import (
	"go_module/cloak"
	"go_module/log"
)
import "strings"

func StartCloakClient(localHost string, localPort string, config string, udp bool) (result int32) {
	defer guardExport("StartCloakClient")()
	clearLastError()
	result = -1

	if err := cloak.StartCloakClient(strings.Clone(localHost), strings.Clone(localPort), strings.Clone(config), udp); err != nil {
		setLastError(err.Error())
		log.Debugf("kotlin_exports", "StartCloakClient failed: %v", err)
		return result
	}
	return 0
}

func StopCloakClient() {
	cloak.StopCloakClient()
}
