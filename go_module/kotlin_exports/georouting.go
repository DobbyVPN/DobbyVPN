//go:build android

package main

import "C"
import (
	"go_module/tunnel"
	"strings"
)

//export SetGeoRoutingConf
func SetGeoRoutingConf(cidrs string) {
	tunnel.SetGeoRoutingConf(strings.Clone(cidrs))
}

//export ClearGeoRoutingConf
func ClearGeoRoutingConf() {
	tunnel.ClearGeoRoutingConf()
}
