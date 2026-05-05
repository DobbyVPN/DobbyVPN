package cloak_outline

import (
	"go_module/log"
	"go_module/tunnel"
)

func SetGeoRoutingConf(cidrs string) {
	defer guard("SetGeoRoutingConf")()
	log.Infof("[ios_exports] SetGeoRoutingConf begin len=%d", len(cidrs))
	tunnel.SetGeoRoutingConf(cidrs)
	log.Infof("[ios_exports] SetGeoRoutingConf returned len=%d", len(cidrs))
}

func ClearGeoRoutingConf() {
	defer guard("ClearGeoRoutingConf")()
	log.Infof("[ios_exports] ClearGeoRoutingConf begin")
	tunnel.ClearGeoRoutingConf()
	log.Infof("[ios_exports] ClearGeoRoutingConf returned")
}
