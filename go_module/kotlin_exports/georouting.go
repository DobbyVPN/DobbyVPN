//go:build android

package dobbyvpn

import (
	"go_module/tunnel"
	"strings"
)

func SetGeoRoutingConf(cidrs string) {
	tunnel.SetGeoRoutingConf(strings.Clone(cidrs))
}

func ClearGeoRoutingConf() {
	tunnel.ClearGeoRoutingConf()
}
