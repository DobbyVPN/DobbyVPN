//go:build ios

package cloak_outline

import (
	"go_module/log"
	"go_module/vpnmanager"
)

func ClearDNSCache() {
	vpnmanager.ClearDNSCache()
}

func SetDNSCacheEntries(entries string) int32 {
	count := vpnmanager.SetDNSCacheEntries(entries, "ios-preflight")
	log.Debugf(logCategory, "SetDNSCacheEntries cached=%d source=ios-preflight", count)
	return count
}
