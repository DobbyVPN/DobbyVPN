//go:build android

package dobbyvpn

import (
	"go_module/log"
	"strings"
)

func InitLogger(path string) {
	log.SetPath(strings.Clone(path))
}

func InitTelemetry(endpoint string) {
	log.InitTelemetry(strings.Clone(endpoint))
}
