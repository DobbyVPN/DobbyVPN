//go:build darwin && !ios

package runtimebridge

import (
	"core/routing"
	"core/sessionapi"
	"core/sessionapi/runtime"
	"core/tunnel/platform_engine"
)

func NewPacketFlow(device *platform_engine.PacketFlowDevice, physical routing.MacOSPhysicalNetwork) sessionapi.Runtime {
	return runtime.NewPacketFlow(device, physical, newDevice)
}
