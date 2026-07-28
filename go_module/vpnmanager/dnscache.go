package vpnmanager

import (
	"math"

	"go_module/dnscache"
)

func ClearDNSCache() {
	dnscache.Clear()
}

func SetDNSCacheEntries(entries, source string) int32 {
	if source == "" {
		source = "preflight"
	}
	count := dnscache.SetEntries(entries, source, dnscache.PreflightCacheTTL)
	if count > math.MaxInt32 {
		return math.MaxInt32
	}
	if count < math.MinInt32 {
		return math.MinInt32
	}
	return int32(count) // #nosec G115 -- bounds checked immediately above.
}
