//go:build !(android || ios)

package api

import (
	"go_module/log"
)

func InitLogger(path string) {
	log.SetPath(path)
}

func InitTelemetry(endpoint, token string) {
	log.InitTelemetry(endpoint, token)
}

func StopTelemetry() {
	log.StopTelemetry()
}

func SetupTelemetryAttributes(config string) {
	log.SetupTelemetryAttributes(config)
}
