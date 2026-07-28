//go:build !(android || ios)

package runtimecore

import (
	"io"

	"go_module/core"
	"go_module/core/pkg"
)

const mobileRuntime = false

func newPlatformCore(device pkg.ProtocolDevice, _ io.ReadWriteCloser) coreClient {
	return core.NewClient(device)
}
