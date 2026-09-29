//go:build !(android || ios)

package runtime

import (
	"io"

	"core/dnscache"
	"core/protocol"
	"core/tunnel"
)

const mobileRuntime = false

func newPlatformCore(device protocol.ProtocolDevice, _ io.ReadWriteCloser, dnsCache *dnscache.Cache, bypass *tunnel.BypassPolicy) sessionCore {
	return newNativeRuntime(device, dnsCache, bypass)
}
