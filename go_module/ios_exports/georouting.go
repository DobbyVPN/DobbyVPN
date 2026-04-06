package cloak_outline

import "go_module/tunnel"

func SetGeoRoutingConf(cidrs string) {
	tunnel.SetGeoRoutingConf(cidrs)
}

func ClearGeoRoutingConf() {
	tunnel.ClearGeoRoutingConf()
}
