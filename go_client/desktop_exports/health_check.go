package main

import "C"
import (
	"go_client/common"
	"go_client/healthcheck"
	log "go_client/logger"
)

//export StartHealthCheck
func StartHealthCheck(period int, sendMetrics bool) {
	healthcheck.StartHealthCheck(int32(period), sendMetrics)
}

//export StopHealthCheck
func StopHealthCheck() {
	healthcheck.StopHealthCheck()
}

//export Status
func Status() string {
	return healthcheck.Status()
}

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
