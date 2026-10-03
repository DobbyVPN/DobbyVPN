//go:build linux || android || ios

package platform_engine

import (
	"context"
	"runtime"
	"strconv"

	"github.com/xjasonlyu/tun2socks/v2/core/device"
	"github.com/xjasonlyu/tun2socks/v2/core/device/fdbased"
)

func startPlatformEngine(cfg EngineConfig) (bool, error) {
	offset := 0
	if runtime.GOOS == "ios" {
		offset = 4
	}
	return startStack(cfg.ProxyAddr, func() (device.Device, error) {
		return fdbased.Open(strconv.Itoa(cfg.FD), 1200, offset)
	})
}

func stopPlatformEngine(_ context.Context, stopDevice func()) error { stopDevice(); return nil }
func platformInterfaceName() string                                 { return "" }
