//go:build android

package main

import "C"
import "go_module/tunnel"

//export SetGeoRoutingConf
func SetGeoRoutingConf(cidrs string) {
	tunnel.SetGeoRoutingConf(cidrs)
}

//export ClearGeoRoutingConf
func ClearGeoRoutingConf() {
	tunnel.ClearGeoRoutingConf()
}
