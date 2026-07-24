//go:build !(android || ios)

package api

import (
	"context"

	"go_module/sessionapi/desktopbinding"
	"go_module/sessionapi/v1"
)

// These legacy direct exports delegate to the same process-wide manager as
// desktop gRPC. They retain no CoreClient, protocol, or last-error state.
func GetVpnLastError() string {
	return desktopbinding.Default().LegacyLastFailure(context.Background())
}
func StartVpn(config, protocol string) int32 {
	if err := desktopbinding.Default().StartLegacy(context.Background(), legacyProtocol(protocol), config); err != nil {
		return -1
	}
	return 0
}
func StopVpn() { _ = desktopbinding.Default().StopLegacy(context.Background()) }
func legacyProtocol(protocol string) v1.Protocol {
	switch protocol {
	case "outline":
		return v1.ProtocolOutline
	case "xray":
		return v1.ProtocolXray
	case "trusttunnel":
		return v1.ProtocolTrustTunnel
	default:
		return v1.Protocol("")
	}
}
