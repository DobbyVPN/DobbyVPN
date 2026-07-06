//go:build !(android || ios)

package api

import "go_module/dnscache"

func ClearDNSCache() {
	dnscache.Clear()
}

func SetDNSCacheEntries(entries, source string) int32 {
	if source == "" {
		source = "desktop-preflight"
	}
	return int32(dnscache.SetEntries(entries, source, dnscache.PreflightCacheTTL))
}
