//go:build android

package dobbyvpn

import "go_module/vpnmanager"

func ClearDNSCache() {
	vpnmanager.ClearDNSCache()
}

func SetDNSCacheEntries(entries string) int32 {
	return vpnmanager.SetDNSCacheEntries(entries, "android-preflight")
}
