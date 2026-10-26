//go:build android || ios

package runtime

import (
	"io"

	"core/dnscache"
	"core/tunnel"
)

const mobileRuntime = true

func newPlatformCore(tun io.ReadWriteCloser, dnsCache *dnscache.Cache, bypass *tunnel.BypassPolicy) sessionCore {
	return newNativeRuntime(tun, dnsCache, bypass)
}
