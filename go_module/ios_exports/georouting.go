package cloak_outline

import (
	"go_module/log"
	"go_module/tunnel"
)

func SetGeoRoutingConf(cidrs string) {
	defer guard("SetGeoRoutingConf")()
	log.Debugf(Category, "SetGeoRoutingConf begin len=%d", len(cidrs))
	tunnel.SetGeoRoutingConf(cidrs)
	log.Debugf(Category, "SetGeoRoutingConf returned len=%d", len(cidrs))
}

func ClearGeoRoutingConf() {
	defer guard("ClearGeoRoutingConf")()
	log.Debugf(Category, "ClearGeoRoutingConf begin")
	tunnel.ClearGeoRoutingConf()
	log.Debugf(Category, "ClearGeoRoutingConf returned")
}
