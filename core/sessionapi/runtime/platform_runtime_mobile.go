//go:build android || ios

package runtime

import (
	"io"

	"core/protocol"
)

const mobileRuntime = true

func newPlatformCore(device protocol.ProtocolDevice, tun io.ReadWriteCloser) sessionCore {
	return newNativeRuntime(device, tun)
}
