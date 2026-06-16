//go:build ios

package cloak_outline

import (
	"go_module/healthcheck"
	"go_module/log"
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
	log.Debugf("ios_exports", "Init health check")
	healthcheck.InitHealthCheck()
}

func StartHealthCheck() {
	log.Debugf("ios_exports", "Start health check")
	healthcheck.StartHealthCheck()
}

func StopHealthCheck() {
	log.Debugf("ios_exports", "Stop health check")
	healthcheck.StopHealthCheck()
}
