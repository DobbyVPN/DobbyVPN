package main

import "C"
import (
	"go_module/healthcheck"
)

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
