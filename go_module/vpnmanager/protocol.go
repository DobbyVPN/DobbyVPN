package vpnmanager

import (
	"fmt"

	"go_module/core/pkg"
	"go_module/log"
	"go_module/outline"
	"go_module/trusttunnel"
	"go_module/xray"
)

// NewProtocolDevice creates a protocol engine from config and protocol name.
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
			log.Debugf(logCategory, "NewProtocolDevice failed: unsupported protocol %q", protocol)
		}
		return nil, fmt.Errorf("unsupported protocol: %s", protocol)
	}
}
