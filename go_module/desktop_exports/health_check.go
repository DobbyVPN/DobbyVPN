package main

import "C"
import (
	"go_module/common"
	"go_module/healthcheck"
	"go_module/log"
)

//export TcpPing
func TcpPing(address string) (int32, error) {
	return healthcheck.TCPPing(address)
}

//export UrlTest
func UrlTest(url string, standard int) (int32, error) {
	return healthcheck.URLTest(url, standard)
}

//export CouldStart
func CouldStart() bool {
	log.Infof("Call CouldStart: %v", common.Client.CouldStart())
	return common.Client.CouldStart()
}

//export CheckServerAlive
func CheckServerAlive(addressC *C.char, port C.int) C.int {
	address := C.GoString(addressC)
	res := healthcheck.CheckServerAlive(address, int(port))
	log.Infof("Health check result: %v", res)
	if res == nil {
		return 0
	}
	return -1
}
