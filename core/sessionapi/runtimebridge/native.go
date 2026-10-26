// Package runtimebridge wires the pure session runtime to DobbyVPN's native
// protocol implementations. Keeping this wiring separate lets ordinary Go
// lifecycle tests run without optional TrustTunnel C++ libraries.
package runtimebridge

import (
	"context"
	"fmt"

	"core/dnscache"
	"core/outline"
	vpnprotocol "core/protocol"
	"core/sessionapi"
	"core/sessionapi/runtime"
	"core/trusttunnel"
	"core/xray"
)

// New installs all supported native protocols while retaining the runtime's
// transactional lifecycle, probing, tun2socks, routing, and DNS behavior.
func New(tunnel runtime.TunnelProvider) sessionapi.Runtime {
	return runtime.New(runtime.Options{
		Tunnel:    tunnel,
		NewDevice: newDevice,
	})
}

func newDevice(
	ctx context.Context,
	_ sessionapi.SessionRef,
	profile sessionapi.RuntimeProfile,
	_ runtime.SocketProtector,
	dnsCache *dnscache.Cache,
) (vpnprotocol.ProtocolDevice, error) {
	switch profile.Summary.Protocol {
	case sessionapi.ProtocolOutline:
		return outline.NewOutlineDevice(ctx, profile.Config.OutlineURL, dnsCache)
	case sessionapi.ProtocolXray:
		return xray.NewXrayDevice(ctx, profile.Config.Xray, dnsCache)
	case sessionapi.ProtocolTrustTunnel:
		return trusttunnel.NewTrustTunnelDevice(ctx, profile.Config.TrustTunnel, dnsCache)
	default:
		return nil, fmt.Errorf("unsupported protocol")
	}
}
