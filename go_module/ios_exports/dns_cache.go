//go:build ios

package cloak_outline

import (
	"time"

	"go_module/dnscache"
	"go_module/log"
)

const dnsPreflightCacheTTL = 12 * time.Hour

func ClearDNSCache() {
	dnscache.Clear()
}

func SetDNSCacheEntries(entries string) int32 {
	count := dnscache.SetEntries(entries, "ios-preflight", dnsPreflightCacheTTL)
	log.Debugf("ios_exports", "SetDNSCacheEntries cached=%d source=ios-preflight", count)
	return int32(count)
}
