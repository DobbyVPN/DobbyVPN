//go:build android

package dobbyvpn

import (
	"go_module/cloak"
)
import "strings"

func StartCloakClient(localHost string, localPort string, config string, udp bool) {
	cloak.StartCloakClient(strings.Clone(localHost), strings.Clone(localPort), strings.Clone(config), udp)
}

func StopCloakClient() {
	cloak.StopCloakClient()
}
