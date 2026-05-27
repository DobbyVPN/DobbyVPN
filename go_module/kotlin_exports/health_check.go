//go:build android

package dobbyvpn

import (
	"go_module/healthcheck"
	"go_module/log"
	"strings"
)

func CheckServerAlive(address string, port int32) int32 {
	res := healthcheck.CheckServerAlive(strings.Clone(address), int(port))
	log.Infof("[HC] Health check result: %v", res)
	if res == nil {
		return 0
	}
	return -1
}
