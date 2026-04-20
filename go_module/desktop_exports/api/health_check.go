package api

import (
	"go_module/common"
	"go_module/healthcheck"
	"go_module/log"
)

func CouldStart() bool {
	log.Infof("Call CouldStart: %v", common.Client.CouldStart())
	return common.Client.CouldStart()
}

func CheckServerAlive(address string, port int) int32 {
	res := healthcheck.CheckServerAlive(address, port)
	log.Infof("Health check result: %v", res)
	if res == nil {
		return 0
	}
	return -1
}
