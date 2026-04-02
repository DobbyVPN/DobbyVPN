package cloak_outline

import "go_client/tunnel"

func SetGeoRoutingConf(cidrs string) {
	tunnel.SetGeoRoutingConf(cidrs)
}

func ClearGeoRoutingConf() {
	tunnel.ClearGeoRoutingConf()
}
