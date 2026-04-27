//go:build !(android || ios)

package api

import (
	"go_module/log"
)

func InitLogger(path string) {
	log.SetPath(path)
}

func InitTelemetry(endpoint string) {
	log.SetTelemetry(endpoint)
}
