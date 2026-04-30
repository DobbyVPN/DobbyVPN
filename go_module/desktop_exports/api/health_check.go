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

func GetConnectionState() int32 {
	return int32(healthcheck.GetConnectionState())
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
