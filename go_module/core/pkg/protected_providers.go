package pkg

import (
	"context"
	"fmt"
	"net"

	"go_module/tunnel/protected_dialer"

	"github.com/Jigsaw-Code/outline-sdk/transport"
)

type ProtectedStreamDialer struct{}

func NewProtectedStreamDialer(destination string) *ProtectedStreamDialer {
	return &ProtectedStreamDialer{}
}

func (d *ProtectedStreamDialer) DialStream(ctx context.Context, address string) (transport.StreamConn, error) {
	conn, err := protected_dialer.DialContextWithProtect(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	sc, ok := conn.(transport.StreamConn)
	if !ok {
		conn.Close()
		return nil, fmt.Errorf("protected TCP conn does not implement transport.StreamConn")
	}
	return sc, nil
}

type ProtectedPacketDialer struct{}

func NewProtectedPacketDialer(destination string) *ProtectedPacketDialer {
	return &ProtectedPacketDialer{}
}

func (d *ProtectedPacketDialer) DialPacket(ctx context.Context, address string) (net.Conn, error) {
	// Use DialUDPWithProtect (not NewProtectedDialer.DialContext) so that:
	// 1. The correct normalizeUDP/listenAddr logic is applied.
	// 2. The routing-loop guard (198.18.x.x check) is enforced for UDP too.
	pc, err := protected_dialer.DialUDPWithProtect(ctx, "udp", address)
	if err != nil {
		return nil, err
	}
	// DialUDPWithProtect returns a net.PacketConn (connectedUDPConn) that also
	// implements net.Conn via Write/RemoteAddr — cast is safe here.
	conn, ok := pc.(net.Conn)
	if !ok {
		_ = pc.Close()
		return nil, fmt.Errorf("protected UDP conn does not implement net.Conn")
	}
	return conn, nil
}
