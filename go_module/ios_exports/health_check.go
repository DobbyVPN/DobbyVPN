//go:build ios

package cloak_outline

import (
	"go_module/healthcheck"
	"go_module/log"
	"go_module/vpnmanager"
)

func GetConnectionState() int32 {
	return vpnmanager.ConnectionStateToInt32(healthcheck.GetConnectionState())
}

func InitHealthCheck() {
	log.Debugf(logCategory, "Init health check")
	healthcheck.InitHealthCheck()
}

func StartHealthCheck() {
	log.Debugf(logCategory, "Start health check")
	healthcheck.StartHealthCheck()
}

func StopHealthCheck() {
	log.Debugf(logCategory, "Stop health check")
	healthcheck.StopHealthCheck()
}

func MeasureTunnelProbeAverageLatencyMillis() int64 {
	log.Debugf(logCategory, "Measure tunnel probe average latency")
	return healthcheck.MeasureTunnelProbeAverageLatencyMillis()
}

func MeasureTunnelProbeAverageLatencyMillisWithTimeout(timeoutMillis int64) int64 {
	log.Debugf(logCategory, "Measure tunnel probe average latency timeoutMs=%d", timeoutMillis)
	return healthcheck.MeasureTunnelProbeAverageLatencyMillisWithTimeout(timeoutMillis)
}
