//go:build android || ios

package runtime

import (
	"io"

	"core/dnscache"
	"core/protocol"
	"core/tunnel"
)

const mobileRuntime = true

func newPlatformCore(device protocol.ProtocolDevice, tun io.ReadWriteCloser, dnsCache *dnscache.Cache, bypass *tunnel.BypassPolicy) sessionCore {
	return newNativeRuntime(device, tun, dnsCache, bypass)
}
