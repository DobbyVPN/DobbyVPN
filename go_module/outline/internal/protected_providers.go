package internal

import (
	"context"
	"net"

	"github.com/Jigsaw-Code/outline-sdk/transport"
	"go_module/tunnel/protected_dialer"
)

// ProtectedStreamDialer is a transport.StreamDialer that uses protected_dialer.
type ProtectedStreamDialer struct {
	destination string
}

func NewProtectedStreamDialer(destination string) *ProtectedStreamDialer {
	return &ProtectedStreamDialer{destination: destination}
}

func (d *ProtectedStreamDialer) DialStream(ctx context.Context, address string) (net.Conn, error) {
	return protected_dialer.DialContextWithProtect(ctx, "tcp", address)
}

// ProtectedPacketDialer is a transport.PacketDialer that uses protected_dialer.
type ProtectedPacketDialer struct {
	destination string
}

func NewProtectedPacketDialer(destination string) *ProtectedPacketDialer {
	return &ProtectedPacketDialer{destination: destination}
}

func (d *ProtectedPacketDialer) DialPacket(ctx context.Context, address string) (net.PacketConn, error) {
	return protected_dialer.DialUDPWithProtect(ctx, "udp", address)
}
