package cloak_outline

import (
	"go_module/healthcheck"
)

func GetConnectionState() int32 {
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

func InitHealthCheck() {
	healthcheck.InitHealthCheck()
}

func StartHealthCheck() {
	healthcheck.StartHealthCheck()
}

func StopHealthCheck() {
	healthcheck.StopHealthCheck()
}
