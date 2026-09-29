//go:build !(android || ios)

package runtime

import (
	"io"

	"core/protocol"
)

const mobileRuntime = false

func newPlatformCore(device protocol.ProtocolDevice, _ io.ReadWriteCloser) sessionCore {
	return newNativeRuntime(device)
}
