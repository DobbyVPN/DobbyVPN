//go:build ios

package cloak_outline

import "go_module/vpnmanager"

func SetGeoRoutingConf(cidrs string) {
	vpnmanager.SetGeoRoutingConf(cidrs)
}

func ClearGeoRoutingConf() {
	vpnmanager.ClearGeoRoutingConf()
}
