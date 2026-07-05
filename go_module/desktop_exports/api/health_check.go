//go:build !(android || ios)

package api

import (
	"go_module/common"
	apiCommon "go_module/desktop_exports/common"
	"go_module/healthcheck"
	"go_module/log"
)

func CouldStart() bool {
	log.Debugf(apiCommon.Category, "Call CouldStart: %v", common.Client.CouldStart())
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

func MeasureTunnelProbeAverageLatencyMillis(timeoutMillis int64) int64 {
	return healthcheck.MeasureTunnelProbeAverageLatencyMillisWithTimeout(timeoutMillis)
}
