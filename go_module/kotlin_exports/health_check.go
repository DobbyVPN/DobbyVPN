//go:build android

package main

import "C"
import (
	"go_module/healthcheck"
)

<<<<<<< HEAD
//export GetConnectionState
func GetConnectionState() C.int {
	switch healthcheck.GetConnectionState() {
	case healthcheck.Disconnected:
		return 0
	case healthcheck.Connecting:
		return 1
	case healthcheck.Connected:
		return 2
	default:
=======
//export CheckServerAlive
func CheckServerAlive(addressC *C.char, port C.int) C.int {
	address := C.GoString(addressC)
	res := healthcheck.CheckServerAlive(address, int(port))
	log.Debugf(Category, "Health check result: %v", res)
	if res == nil {
>>>>>>> category-logging
		return 0
	}
}

//export InitHealthCheck
func InitHealthCheck() {
	healthcheck.InitHealthCheck()
}

//export StartHealthCheck
func StartHealthCheck() {
	healthcheck.StartHealthCheck()
}

//export StopHealthCheck
func StopHealthCheck() {
	healthcheck.StopHealthCheck()
}
