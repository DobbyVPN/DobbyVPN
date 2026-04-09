//go:build android

package main

import "C"
import "go_module/tunnel"

//export SetGeoRoutingConf
func SetGeoRoutingConf(cidrsC *C.char) {
	tunnel.SetGeoRoutingConf(C.GoString(cidrsC))
}

//export ClearGeoRoutingConf
func ClearGeoRoutingConf() {
	tunnel.ClearGeoRoutingConf()
}
