package pkg

import (
	"net"
	"time"
)

// ProtocolDevice represents any VPN protocol implementation (Xray, Outline, etc.)
// that provides a local SOCKS5 bridge for tun2socks.
type ProtocolDevice interface {
	// Open starts the protocol engine.
	Open(routingTableID int, uplinkIface string) error

	// GetProxyAddr returns the local address (e.g., "127.0.0.1:1080")
	// where tun2socks should forward device traffic.
	GetProxyAddr() string

	GetServerIP() net.IP

	// Close shuts down the engine and releases bound ports.
	Close() error
}

// ProxyStatusProvider is an optional interface that protocol devices may implement
// to report whether their local SOCKS5 proxy is alive. CoreClient uses this to
// include localProxyAlive=true/false in the VpnStatus string, which the health-check
// layer reads via XPC.
type ProxyStatusProvider interface {
	// Status returns a human-readable string that MUST contain either
	// "localProxyAlive=true" or "localProxyAlive=false".
	Status(timeout time.Duration) string
}
