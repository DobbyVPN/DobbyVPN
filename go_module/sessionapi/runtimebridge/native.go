// Package runtimebridge wires the pure session runtime to DobbyVPN's native
// protocol implementations. Keeping this wiring separate lets ordinary Go
// lifecycle tests run without optional TrustTunnel C++ libraries.
package runtimebridge

import (
	"context"
	"fmt"

	"go_module/outline"
	vpnprotocol "go_module/protocol"
	"go_module/sessionapi/runtime"
	v2 "go_module/sessionapi/v2"
	"go_module/trusttunnel"
	"go_module/xray"
)

// New installs all supported native protocols while retaining the runtime's
// transactional lifecycle, probing, tun2socks, routing, and DNS behavior.
func New(tunnel runtime.TunnelProvider) v2.Runtime {
	return runtime.New(runtime.Options{
		Tunnel:    tunnel,
		NewDevice: newDevice,
	})
}

func newDevice(_ context.Context, _ v2.SessionRef, profile v2.RuntimeProfile, _ runtime.SocketProtector) (vpnprotocol.ProtocolDevice, error) {
	config := string(profile.NormalizedConfig)
	switch profile.Summary.Protocol {
	case v2.ProtocolOutline:
		return outline.NewOutlineDevice(config)
	case v2.ProtocolXray:
		return xray.NewXrayDevice(config)
	case v2.ProtocolTrustTunnel:
		return trusttunnel.NewTrustTunnelDevice(config)
	default:
		return nil, fmt.Errorf("unsupported protocol")
	}
}
