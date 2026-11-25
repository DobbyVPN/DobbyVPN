//go:build !(android || ios || darwin)

package runtime

import (
	"io"

	"core/dnscache"
)

const mobileRuntime = false

func newPlatformCore(_ io.ReadWriteCloser, dnsCache *dnscache.Cache) sessionCore {
	return newNativeRuntime(dnsCache)
}
