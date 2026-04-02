package main

import "C"
import "go_client/tunnel"

//export SetGeoRoutingConf
func SetGeoRoutingConf(cidrsC *C.char) {
	tunnel.SetGeoRoutingConf(C.GoString(cidrsC))
}

//export ClearGeoRoutingConf
func ClearGeoRoutingConf() {
	tunnel.ClearGeoRoutingConf()
}
