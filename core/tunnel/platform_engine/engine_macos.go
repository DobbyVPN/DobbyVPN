//go:build darwin && !(android || ios)

package platform_engine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/xjasonlyu/tun2socks/v2/core/device"
	"github.com/xjasonlyu/tun2socks/v2/core/device/tun"

	"core/log"
)

var lastIface string

const (
	macOSTunReleasePoll = 50 * time.Millisecond
)

func startPlatformEngine(c EngineConfig) (bool, error) {
	accepted, err := startStack(c, func() (device.Device, error) { return tun.Open("utun", TunnelMTU) })
	if !accepted {
		return false, err
	}
	if ownedDevice != nil {
		lastIface = ownedDevice.Name()
	}
	if err != nil {
		return true, err
	}
	if lastIface == "" {
		return true, fmt.Errorf("new utun has no interface name")
	}
	log.Debugf(Category, "[Engine][Darwin] acquired interface=%s", lastIface)
	if err := configureMacOSInterface(lastIface); err != nil {
		return true, err
	}
	return true, nil
}

func stopPlatformEngine(ctx context.Context, stopDevice func(context.Context) error) error {
	deviceName := lastIface
	stopErr := stopDevice(ctx)
	if deviceName == "" {
		return stopErr
	}

	for {
		interfaces, err := net.Interfaces()
		if err != nil {
			return errors.Join(stopErr, fmt.Errorf("list interfaces while releasing macOS TUN %s: %w", deviceName, err))
		}
		found := false
		for _, iface := range interfaces {
			if iface.Name == deviceName {
				found = true
				break
			}
		}
		if !found {
			lastIface = ""
			return stopErr
		}
		if ctx.Err() != nil {
			return errors.Join(stopErr, fmt.Errorf("macOS TUN %s remained after tun2socks stopped", deviceName))
		}
		select {
		case <-ctx.Done():
		case <-time.After(macOSTunReleasePoll):
		}
	}
}

func platformInterfaceName() string { return lastIface }
