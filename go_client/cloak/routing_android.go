//go:build android
// +build android

package cloak

// StartRoutingCloak is intentionally empty on Android.
// Unlike Linux/desktop platforms, Android VPN service manages routing at the system level
// through the VPN interface. Adding manual IP routes would conflict with the VPN's
// built-in traffic routing and cause DNS resolution failures and network connectivity issues.
// The Android VPN framework automatically handles traffic routing for all apps through
// the tunnel, so no additional routing configuration is needed.
func StartRoutingCloak(proxyIP string) error {
	return nil
}

// StopRoutingCloak is intentionally empty on Android.
// Since StartRoutingCloak doesn't add any routes on Android, there's nothing to clean up.
// The Android VPN service automatically handles route cleanup when the VPN disconnects.
func StopRoutingCloak(proxyIP string) {
}
