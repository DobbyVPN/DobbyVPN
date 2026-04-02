//go:build android

package protected_dialer

import "C"
import (
	"context"
	"go_client/log"
	"net"
	"syscall"
)

var MakeSocketProtected func(fd uintptr)

func DialContextWithProtect(ctx context.Context, network string, address string) (net.Conn, error) {
	d := &net.Dialer{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				MakeSocketProtected(fd)
			})
		},
	}
	return d.DialContext(ctx, network, address)
}

func DialUDPWithProtect(ctx context.Context, network string, address string) (net.PacketConn, error) {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				MakeSocketProtected(fd)
			})
		},
	}

	pc, err := lc.ListenPacket(ctx, network, ":0")
	if err != nil {
		return nil, err
	}

	udpAddr, err := net.ResolveUDPAddr(network, address)
	if err != nil {
		pc.Close()
		return nil, err
	}

	log.Infof("[Go] UDP Socket protected and bound for %s", address)

	return &connectedUDPConn{
		PacketConn: pc,
		remoteAddr: udpAddr,
	}, nil
}

type connectedUDPConn struct {
	net.PacketConn
	remoteAddr net.Addr
}

func (c *connectedUDPConn) Write(b []byte) (int, error) {
	return c.WriteTo(b, c.remoteAddr)
}

func (c *connectedUDPConn) RemoteAddr() net.Addr {
	return c.remoteAddr
}
