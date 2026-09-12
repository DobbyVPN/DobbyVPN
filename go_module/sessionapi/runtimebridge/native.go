// Package runtimebridge wires the pure session runtime to DobbyVPN's native
// protocol implementations. Keeping this wiring separate lets ordinary Go
// lifecycle tests run without optional TrustTunnel C++ libraries.
package runtimebridge

import (
	"context"
	"fmt"

	vpnprotocol "go_module/protocol"
	"go_module/sessionapi/native"
	"go_module/sessionapi/runtime"
	v2 "go_module/sessionapi/v2"
)

const category = "sessionapi/runtimebridge"

// New installs all supported native protocols while retaining the runtime's
// transactional lifecycle, probing, tun2socks, routing, and DNS behavior.
func New(tunnel runtime.TunnelProvider) v2.Runtime {
	return runtime.New(runtime.Options{
		Tunnel:    tunnel,
		NewDevice: newDevice,
	})
}

func newDevice(_ context.Context, _ v2.SessionRef, profile v2.RuntimeProfile, _ runtime.SocketProtector) (vpnprotocol.ProtocolDevice, error) {
	protocol, ok := map[v2.Protocol]string{
		v2.ProtocolOutline:     "outline",
		v2.ProtocolXray:        "xray",
		v2.ProtocolTrustTunnel: "trusttunnel",
	}[profile.Summary.Protocol]
	if !ok {
		return nil, fmt.Errorf("unsupported protocol")
	}
	return native.NewProtocolDevice(string(profile.NormalizedConfig), protocol, category)
}
