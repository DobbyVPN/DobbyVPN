//go:build android || ios

package runtime

import (
	"io"

	"go_module/core"
	"go_module/core/pkg"
)

const mobileRuntime = true

func newPlatformCore(device pkg.ProtocolDevice, tun io.ReadWriteCloser) sessionCore {
	return core.NewNativeRuntime(device, tun)
}
