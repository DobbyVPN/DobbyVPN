package protected_dialer

import (
	"context"
	"go_client/log"
	"net"
	"syscall"
)

const (
	SO_NO_TC_NETPOLICY = 0x1101
)

func DialContextWithProtect(ctx context.Context, network string, address string) (net.Conn, error) {
	log.Infof("[iOS-Protect] Dialing TCP: %s", address)
	d := &net.Dialer{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				log.Infof("[iOS-Protect] TCP Control: Protecting FD %d for %s", fd, address)
				setNoNetworkPolicy(fd)
			})
		},
	}

	conn, err := d.DialContext(ctx, network, address)
	if err != nil {
		log.Infof("[iOS-Protect] TCP Dial FAILED for %s: %v", address, err)
		return nil, err
	}
	log.Infof("[iOS-Protect] TCP Dial SUCCESS: %s (Local: %s)", address, conn.LocalAddr())
	return conn, nil
}

func DialUDPWithProtect(ctx context.Context, network string, address string) (net.PacketConn, error) {
	log.Infof("[iOS-Protect] Dialing UDP: %s", address)
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				log.Infof("[iOS-Protect] UDP Control: Protecting FD %d for %s", fd, address)
				setNoNetworkPolicy(fd)
			})
		},
	}

	pc, err := lc.ListenPacket(ctx, network, ":0")
	if err != nil {
		log.Infof("[iOS-Protect] UDP ListenPacket FAILED: %v", err)
		return nil, err
	}

	udpAddr, err := net.ResolveUDPAddr(network, address)
	if err != nil {
		log.Infof("[iOS-Protect] UDP ResolveAddr FAILED for %s: %v", address, err)
		pc.Close()
		return nil, err
	}

	log.Infof("[iOS-Protect] UDP Socket READY for %s", address)

	return &connectedUDPConn{
		PacketConn: pc,
		remoteAddr: udpAddr,
		target:     address,
	}, nil
}

func setNoNetworkPolicy(fd uintptr) {
	err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, SO_NO_TC_NETPOLICY, 1)
	if err != nil {
		log.Infof("[iOS-Protect] Setsockopt ERR on FD %d: %v", fd, err)
	} else {
		log.Infof("[iOS-Protect] Setsockopt OK on FD %d", fd)
	}
}

type connectedUDPConn struct {
	net.PacketConn
	remoteAddr net.Addr
	target     string
}

func (c *connectedUDPConn) Write(b []byte) (int, error) {
	n, err := c.WriteTo(b, c.remoteAddr)
	if err != nil {
		log.Infof("[iOS-Protect] UDP Write to %s FAILED: %v", c.target, err)
	}
	return n, err
}

func (c *connectedUDPConn) RemoteAddr() net.Addr {
	return c.remoteAddr
}
