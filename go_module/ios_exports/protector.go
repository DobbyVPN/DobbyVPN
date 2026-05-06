//go:build ios
// +build ios

package cloak_outline

import (
	"go_module/log"
	"go_module/tunnel/protected_dialer"
)

// SetDefaultInterfaceIndex sets the default network interface index for socket protection.
// This is called from Swift when the default interface changes (WiFi/Cellular).
// On iOS 26+, SO_NO_TC_NETPOLICY no longer works, so we use IP_BOUND_IF instead.
func SetDefaultInterfaceIndex(index int) {
	defer guard("SetDefaultInterfaceIndex")()
	log.Infof("[iOS-Protect] Setting default interface index: %d", index)
	protected_dialer.SetDefaultInterfaceForIOS(index)
	log.Infof("[iOS-Protect] SetDefaultInterfaceIndex returned index=%d current=%d", index, protected_dialer.GetDefaultInterfaceForIOS())
}

// GetDefaultInterfaceIndex returns the current default interface index.
// Useful for diagnostics from Swift.
func GetDefaultInterfaceIndex() (index int) {
	defer guard("GetDefaultInterfaceIndex")()
	index = protected_dialer.GetDefaultInterfaceForIOS()
	log.Infof("[iOS-Protect] GetDefaultInterfaceIndex returned index=%d", index)
	return index
}

// ProtectionDiagnostics returns the current Go-side socket protection state.
// Swift logs this at every critical tunnel stage so stale native artifacts and
// interface-index propagation failures are visible in the device log.
func ProtectionDiagnostics() (diagnostics string) {
	defer guard("ProtectionDiagnostics")()
	diagnostics = protected_dialer.ProtectionDiagnosticsForIOS()
	log.Infof("[iOS-Protect] ProtectionDiagnostics returned %s", diagnostics)
	return diagnostics
}
