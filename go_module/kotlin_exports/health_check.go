//go:build android

package main

import "C"
import (
	"go_module/healthcheck"
	"go_module/log"
)

//export CheckServerAlive
func CheckServerAlive(address string, port int32) C.int {
	res := healthcheck.CheckServerAlive(address, int(port))
	log.Infof("[HC] Health check result: %v", res)
	if res == nil {
		return 0
	}
	return -1
}
