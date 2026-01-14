package main

import "C"
import (
	"go_client/healthcheck"
	log "go_client/logger"
)

func StartHealthCheck(period int, sendMetrics bool) {
	healthcheck.StartHealthCheck(period, sendMetrics)
}

func StopHealthCheck() {
	healthcheck.StopHealthCheck()
}

func Status() string {
	return healthcheck.Status()
}

func TcpPing(address string) (int32, error) {
	return healthcheck.TcpPing(address)
}

func UrlTest(url string, standard int) (int32, error) {
	return healthcheck.UrlTest(url, standard)
}

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
