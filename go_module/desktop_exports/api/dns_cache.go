//go:build !(android || ios)

package api

import "go_module/vpnmanager"

func ClearDNSCache() {
	vpnmanager.ClearDNSCache()
}

func SetDNSCacheEntries(entries, source string) int32 {
	if source == "" {
		source = "desktop-preflight"
	}
	return vpnmanager.SetDNSCacheEntries(entries, source)
}
