//go:build ios

package cloak_outline

import (
	"go_module/dnscache"
	"go_module/log"
)

func ClearDNSCache() {
	dnscache.Clear()
}

func SetDNSCacheEntries(entries string) int32 {
	count := dnscache.SetEntries(entries, "ios-preflight", dnscache.PreflightCacheTTL)
	log.Debugf("ios_exports", "SetDNSCacheEntries cached=%d source=ios-preflight", count)
	return int32(count)
}
