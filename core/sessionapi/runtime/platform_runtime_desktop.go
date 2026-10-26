//go:build !(android || ios)

package runtime

import (
	"io"

	"core/dnscache"
	"core/tunnel"
)

const mobileRuntime = false

func newPlatformCore(_ io.ReadWriteCloser, dnsCache *dnscache.Cache, bypass *tunnel.BypassPolicy) sessionCore {
	return newNativeRuntime(dnsCache, bypass)
}
