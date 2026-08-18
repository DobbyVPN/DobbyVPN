// Package native is the SessionV2 composition root for protocol devices.
package native

import (
	"fmt"

	"go_module/core/pkg"
	"go_module/log"
	"go_module/outline"
	"go_module/trusttunnel"
	"go_module/xray"
)

// NewProtocolDevice creates one protocol engine for one owned runtime lease.
func NewProtocolDevice(config, protocol, logCategory string) (pkg.ProtocolDevice, error) {
	switch protocol {
	case "xray":
		return xray.NewXrayDevice(config)
	case "outline":
		return outline.NewOutlineDevice(config)
	case "trusttunnel":
		return trusttunnel.NewTrustTunnelDevice(config)
	default:
		if logCategory != "" {
			log.Debugf(logCategory, "unsupported protocol device")
		}
		return nil, fmt.Errorf("unsupported protocol")
	}
}
