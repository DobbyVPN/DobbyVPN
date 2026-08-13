//go:build android

package dobbyvpn

import (
	"go_module/log"
)

func InitLogger(path string) bool {
	return log.SetPath(path) == nil
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
