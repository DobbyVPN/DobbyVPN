// Package runtimebridge wires the pure session runtime to DobbyVPN's native
// protocol implementations. Keeping this wiring separate lets ordinary Go
// lifecycle tests run without optional TrustTunnel C++ libraries.
package runtimebridge

import (
	"context"
	"fmt"

	"go_module/core/pkg"
	"go_module/sessionapi/runtimecore"
	v1 "go_module/sessionapi/v1"
	"go_module/vpnmanager"
)

const category = "sessionapi/runtimebridge"

// New installs all supported native protocols while retaining runtimecore's
// transactional lifecycle, probing, tun2socks, routing, and DNS behavior.
func New(tunnel runtimecore.TunnelProvider) v1.Runtime {
	return runtimecore.New(runtimecore.Options{
		Tunnel:     tunnel,
		NewDevice:  newDevice,
		StartCloak: startCloak,
	})
}

func newDevice(_ context.Context, _ v1.SessionRef, profile v1.RuntimeProfile, _ runtimecore.SocketProtector) (pkg.ProtocolDevice, error) {
	protocol, ok := map[v1.Protocol]string{
		v1.ProtocolOutline:     "outline",
		v1.ProtocolXray:        "xray",
		v1.ProtocolTrustTunnel: "trusttunnel",
	}[profile.Summary.Protocol]
	if !ok {
		return nil, fmt.Errorf("unsupported protocol")
	}
	return vpnmanager.NewProtocolDevice(string(profile.NormalizedConfig), protocol, category)
}

func startCloak(ctx context.Context, _ v1.SessionRef, raw []byte) (func(context.Context) error, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	config, err := runtimecore.NormalizeCloakProfile(raw)
	if err != nil {
		return nil, err
	}
	if err := vpnmanager.StartCloakClient(category, "127.0.0.1", "1984", string(config), false); err != nil {
		return nil, err
	}
	return func(context.Context) error {
		vpnmanager.StopCloakClient(category)
		return nil
	}, nil
}
