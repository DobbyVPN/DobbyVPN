//go:build ios

package cloak_outline

import (
	"go_module/log"
)

func InitLogger(path string) {
	defer guard("InitLogger")()
	if err := log.SetPath(path); err != nil {
		log.Debugf("ios_exports", "InitLogger failed: %v", err)
	}
}

func InitTelemetry(endpoint, token string) {
	defer guard("InitTelemetry")()
	if err := log.InitTelemetry(endpoint, token); err != nil {
		log.Debugf("ios_exports", "InitTelemetry failed: %v", err)
	}
}

func StopTelemetry() {
	defer guard("StopTelemetry")()
	log.StopTelemetry()
}

func SetupTelemetryAttributes(config string) {
	defer guard("SetupTelemetryAttributes")()
	log.SetupTelemetryAttributes(config)
}
