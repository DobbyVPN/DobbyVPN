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

// StatusProvider is implemented by protocol devices that can report runtime
// health details without changing the base ProtocolDevice contract.
type StatusProvider interface {
	Status(timeout time.Duration) string
}
