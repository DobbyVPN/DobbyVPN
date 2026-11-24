//go:build !(android || ios)

package runtime

import (
	"io"

	"core/dnscache"
)

const mobileRuntime = false

func newPlatformCore(_ io.ReadWriteCloser, dnsCache *dnscache.Cache) sessionCore {
	return newNativeRuntime(dnsCache)
}
