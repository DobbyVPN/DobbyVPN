//go:build !(android || ios)

package runtime

import (
	"io"

	"go_module/core"
	"go_module/core/pkg"
)

const mobileRuntime = false

func newPlatformCore(device pkg.ProtocolDevice, _ io.ReadWriteCloser) sessionCore {
	return core.NewSession(device)
}
