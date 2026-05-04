//go:build ios
// +build ios

package cloak_outline

import (
	"go_module/tunnel/protected_dialer"
	"go_module/log"
)

// SetDefaultInterfaceIndex sets the default network interface index for socket protection.
// This is called from Swift when the default interface changes (WiFi/Cellular).
// On iOS 26+, SO_NO_TC_NETPOLICY no longer works, so we use IP_BOUND_IF instead.
func SetDefaultInterfaceIndex(index int) {
	log.Infof("[iOS-Protect] Setting default interface index: %d", index)
	protected_dialer.SetDefaultInterfaceForIOS(index)
}

// GetDefaultInterfaceIndex returns the current default interface index.
// Useful for diagnostics from Swift.
func GetDefaultInterfaceIndex() int {
	return protected_dialer.GetDefaultInterfaceForIOS()
}
