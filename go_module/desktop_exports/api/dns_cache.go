//go:build !(android || ios)

package api

import (
	"time"

	"go_module/dnscache"
)

const dnsPreflightCacheTTL = 12 * time.Hour

func ClearDNSCache() {
	dnscache.Clear()
}

func SetDNSCacheEntries(entries, source string) int32 {
	if source == "" {
		source = "desktop-preflight"
	}
	return int32(dnscache.SetEntries(entries, source, dnsPreflightCacheTTL))
}
