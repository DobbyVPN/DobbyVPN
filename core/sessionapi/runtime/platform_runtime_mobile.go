//go:build android || ios

package runtime

import (
	"io"

	"core/dnscache"
)

const mobileRuntime = true

func newPlatformCore(tun io.ReadWriteCloser, _ *dnscache.Cache) sessionCore {
	return newNativeRuntime(tun)
}
