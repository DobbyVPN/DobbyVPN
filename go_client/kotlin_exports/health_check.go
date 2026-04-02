package main

import "C"
import (
	"go_client/healthcheck"
	"go_client/log"
)

//export CheckServerAlive
func CheckServerAlive(addressC *C.char, port C.int) C.int {
	address := C.GoString(addressC)
	res := healthcheck.CheckServerAlive(address, int(port))
	log.Infof("[HC] Health check result: %v", res)
	if res == nil {
		return 0
	}
	return -1
}
