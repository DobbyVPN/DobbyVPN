//go:build android

package dobbyvpn

import (
	"time"

	"go_module/dnscache"
)

const dnsPreflightCacheTTL = 12 * time.Hour

func ClearDNSCache() {
	dnscache.Clear()
}

func SetDNSCacheEntries(entries string) int32 {
	return int32(dnscache.SetEntries(entries, "android-preflight", dnsPreflightCacheTTL))
}
