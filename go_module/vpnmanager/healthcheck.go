package vpnmanager

import "go_module/healthcheck"

// ConnectionStateToInt32 maps healthcheck states to mobile/desktop export values.
func ConnectionStateToInt32(state healthcheck.ConnectionState) int32 {
	switch state {
	case healthcheck.Disconnected:
		return 0
	case healthcheck.Connecting:
		return 1
	case healthcheck.Connected:
		return 2
	default:
		return 0
	}
}
