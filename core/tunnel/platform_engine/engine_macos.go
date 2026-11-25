//go:build darwin && !ios

package platform_engine

import (
	"context"
	"errors"

	stackAdapter "github.com/xjasonlyu/tun2socks/v2/core/adapter"
	"github.com/xjasonlyu/tun2socks/v2/core/device"
)

func startPlatformEngine(cfg EngineConfig, handler stackAdapter.TransportHandler) (bool, error) {
	if cfg.PacketFlow == nil {
		return false, errors.New("macOS requires a Network Extension packet flow")
	}
	return startStack(handler, func() (device.Device, error) { return cfg.PacketFlow, nil })
}

func stopPlatformEngine(ctx context.Context, stopDevice func(context.Context) error) error {
	return stopDevice(ctx)
}

func platformInterfaceName() string { return "" }
