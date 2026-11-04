//go:build linux || android || ios

package platform_engine

import (
	"context"
	"runtime"
	"strconv"

	"core/log"

	"github.com/xjasonlyu/tun2socks/v2/core/device"
	"github.com/xjasonlyu/tun2socks/v2/core/device/fdbased"
)

func startPlatformEngine(cfg EngineConfig) (bool, error) {
	offset := 0
	if runtime.GOOS == "ios" {
		offset = 4
	}
	return startStack(cfg, func() (device.Device, error) {
		return fdbased.Open(strconv.Itoa(cfg.FD), TunnelMTU, offset)
	})
}

func stopPlatformEngine(ctx context.Context, stopDevice func(context.Context) error) error {
	// Linux's fd endpoint borrows the descriptor. Stop its packet writers and
	// interruptible readers before device.Close releases that descriptor for
	// reuse. iOS's io endpoint instead needs device.Close to unblock its reader.
	if (runtime.GOOS == "linux" || runtime.GOOS == "android") && ownedStack != nil {
		// Stack.Wait holds stack locks while removing NICs. Stop the FD reader
		// first, outside those locks: an ingress callback may need FindRoute
		// before it can return. The borrowed descriptor stays open until drain.
		if ownedDevice != nil {
			if err := stopPhaseCtx(ctx, "link-detach", func() { ownedDevice.Attach(nil) }); err != nil {
				return err
			}
		}
		if err := drainStack(ctx); err != nil {
			return err
		}
		log.Debugf(Category, "[Engine][FD] stack_drained=true device_release=starting")
	}
	return stopDevice(ctx)
}
func platformInterfaceName() string { return "" }
