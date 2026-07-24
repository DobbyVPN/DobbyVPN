//go:build android

package dobbyvpn

import (
	"go_module/healthcheck"
	"go_module/vpnmanager"
)

func GetConnectionState() int32 {
	return vpnmanager.ConnectionStateToInt32(healthcheck.GetConnectionState())
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

func MeasureTunnelProbeAverageLatencyMillis() int64 {
	return healthcheck.MeasureTunnelProbeAverageLatencyMillis()
}

func MeasureTunnelProbeAverageLatencyMillisWithTimeout(timeoutMillis int64) int64 {
	return healthcheck.MeasureTunnelProbeAverageLatencyMillisWithTimeout(timeoutMillis)
}
