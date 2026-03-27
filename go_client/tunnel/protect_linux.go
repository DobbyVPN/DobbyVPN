//go:build linux
// +build linux

package tunnel

import (
	"context"
	"fmt"
	"net"
	"syscall"

	"go_client/log"
)

var linuxSocketMark int

func SetLinuxSocketMark(mark int) {
	linuxSocketMark = mark
	log.Infof("[Linux-Protect][State] SO_MARK configured = %d", mark)
}

func applySocketMark(fd uintptr) {
	if linuxSocketMark == 0 {
		log.Infof("[Linux-Protect][Mark][SKIP] mark=0 (NOT CONFIGURED)")
		return
	}

	log.Infof("[Linux-Protect][Mark] Applying SO_MARK=%d to fd=%d", linuxSocketMark, fd)

	err := syscall.SetsockoptInt(
		int(fd),
		syscall.SOL_SOCKET,
		syscall.SO_MARK,
		linuxSocketMark,
	)

	if err != nil {
		log.Infof("[Linux-Protect][Mark][ERROR] fd=%d setsockopt failed: %v", fd, err)
	} else {
		log.Infof("[Linux-Protect][Mark][OK] fd=%d marked with %d", fd, linuxSocketMark)
	}
}

func normalizeTCP(address string) string {
	host, _, _ := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	if ip != nil && ip.To4() == nil {
		return "tcp6"
	}
	return "tcp4"
}

func normalizeUDP(address string) string {
	host, _, _ := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	if ip != nil && ip.To4() == nil {
		return "udp6"
	}
	return "udp4"
}

func DialContextWithMark(ctx context.Context, network, address string) (net.Conn, error) {
	log.Infof("[Linux-Protect][TCP] Dial start: network=%s address=%s", network, address)

	host, port, err := net.SplitHostPort(address)
	if err == nil {
		ip := net.ParseIP(host)
		log.Infof("[Linux-Protect][TCP] Parsed host=%s port=%s ip=%v", host, port, ip)

		if ip != nil && ip.IsLoopback() {
			log.Infof("[Linux-Protect][TCP][BYPASS] Loopback detected → direct dial: %s", address)
			var d net.Dialer
			return d.DialContext(ctx, normalizeTCP(address), address)
		}
	}

	realNet := normalizeTCP(address)
	log.Infof("[Linux-Protect][TCP] Normalized network: %s → %s", network, realNet)

	d := &net.Dialer{
		Control: func(network, address string, c syscall.RawConn) error {
			log.Infof("[Linux-Protect][TCP][Control] Applying mark before connect: %s %s", network, address)

			var controlErr error
			err := c.Control(func(fd uintptr) {
				log.Infof("[Linux-Protect][TCP][Control] Got fd=%d", fd)
				applySocketMark(fd)
			})

			if err != nil {
				log.Infof("[Linux-Protect][TCP][Control][ERROR] RawConn.Control failed: %v", err)
				controlErr = err
			}

			return controlErr
		},
	}

	conn, err := d.DialContext(ctx, realNet, address)
	if err != nil {
		log.Infof("[Linux-Protect][TCP][ERROR] Dial failed: %v", err)
		return nil, err
	}

	log.Infof("[Linux-Protect][TCP][OK] Connected → %s (mark=%d)", address, linuxSocketMark)
	return conn, nil
}

func DialUDPWithMark(ctx context.Context, network, address string) (net.PacketConn, error) {
	log.Infof("[Linux-Protect][UDP] Dial start: network=%s address=%s", network, address)

	host, port, err := net.SplitHostPort(address)
	if err == nil {
		ip := net.ParseIP(host)
		log.Infof("[Linux-Protect][UDP] Parsed host=%s port=%s ip=%v", host, port, ip)

		if ip != nil && ip.IsLoopback() {
			log.Infof("[Linux-Protect][UDP][BYPASS] Loopback → direct UDP")

			realNet := normalizeUDP(address)
			listenAddr := "0.0.0.0:0"
			if realNet == "udp6" {
				listenAddr = "[::]:0"
			}

			pc, err := net.ListenPacket(realNet, listenAddr)
			if err != nil {
				log.Infof("[Linux-Protect][UDP][ERROR] ListenPacket failed: %v", err)
				return nil, err
			}

			udpAddr, err := net.ResolveUDPAddr(realNet, address)
			if err != nil {
				log.Infof("[Linux-Protect][UDP][ERROR] ResolveUDPAddr failed: %v", err)
				_ = pc.Close()
				return nil, err
			}

			log.Infof("[Linux-Protect][UDP][OK] Loopback UDP ready → %s", address)

			return &connectedUDPConn{
				PacketConn: pc,
				remoteAddr: udpAddr,
			}, nil
		}
	}

	realNet := normalizeUDP(address)
	log.Infof("[Linux-Protect][UDP] Normalized network: %s → %s", network, realNet)

	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			log.Infof("[Linux-Protect][UDP][Control] Applying mark before bind: %s %s", network, address)

			var controlErr error
			err := c.Control(func(fd uintptr) {
				log.Infof("[Linux-Protect][UDP][Control] Got fd=%d", fd)
				applySocketMark(fd)
			})

			if err != nil {
				log.Infof("[Linux-Protect][UDP][Control][ERROR] RawConn.Control failed: %v", err)
				controlErr = err
			}

			return controlErr
		},
	}

	listenAddr := "0.0.0.0:0"
	if realNet == "udp6" {
		listenAddr = "[::]:0"
	}

	pc, err := lc.ListenPacket(ctx, realNet, listenAddr)
	if err != nil {
		log.Infof("[Linux-Protect][UDP][ERROR] ListenPacket failed: %v", err)
		return nil, err
	}

	udpAddr, err := net.ResolveUDPAddr(realNet, address)
	if err != nil {
		log.Infof("[Linux-Protect][UDP][ERROR] ResolveUDPAddr failed: %v", err)
		_ = pc.Close()
		return nil, err
	}

	log.Infof("[Linux-Protect][UDP][OK] Socket ready → %s (mark=%d)", address, linuxSocketMark)

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

func EnsureLinuxMarkIsConfigured() error {
	if linuxSocketMark == 0 {
		err := fmt.Errorf("linux socket mark is not configured")
		log.Infof("[Linux-Protect][State][ERROR] %v", err)
		return err
	}

	log.Infof("[Linux-Protect][State][OK] mark=%d ready", linuxSocketMark)
	return nil
}
