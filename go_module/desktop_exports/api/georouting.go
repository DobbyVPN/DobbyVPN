//go:build !(android || ios)

package api

import "go_module/vpnmanager"

func SetGeoRoutingConf(cidrs string) {
	vpnmanager.SetGeoRoutingConf(cidrs)
}

func ClearGeoRoutingConf() {
	vpnmanager.ClearGeoRoutingConf()
}
