//go:build android || ios

package runtimecore

import (
	"io"

	"go_module/core"
	"go_module/core/pkg"
)

const mobileRuntime = true

func newPlatformCore(device pkg.ProtocolDevice, tun io.ReadWriteCloser) coreClient {
	return core.NewClient(device, tun)
}
