package pkg

import "net"

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
