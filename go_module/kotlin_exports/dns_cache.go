//go:build android

package dobbyvpn

import "go_module/dnscache"

func ClearDNSCache() {
	dnscache.Clear()
}

func SetDNSCacheEntries(entries string) int32 {
	return int32(dnscache.SetEntries(entries, "android-preflight", dnscache.PreflightCacheTTL))
}
