//go:build android

package dobbyvpn

import (
	"go_module/vpnmanager"
	"strings"
)

func SetGeoRoutingConf(cidrs string) {
	vpnmanager.SetGeoRoutingConf(strings.Clone(cidrs))
}

func ClearGeoRoutingConf() {
	vpnmanager.ClearGeoRoutingConf()
}
