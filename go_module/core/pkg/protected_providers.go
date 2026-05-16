package pkg

import (
	"context"
	"fmt"
	"net"

	"go_module/tunnel/protected_dialer"

	"golang.getoutline.org/sdk/transport"
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
	dialer := protected_dialer.NewProtectedDialer(address)
	return dialer.DialContext(ctx, "udp", address)
}
