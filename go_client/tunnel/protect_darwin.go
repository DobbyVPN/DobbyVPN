//go:build darwin
// +build darwin

package tunnel

import (
	"context"
	"fmt"
	"net"
	"strings"
	"syscall"

	"go_client/log"
)

const (
	IP_BOUND_IF   = 25
	IPV6_BOUND_IF = 125
)

var defaultInterfaceIndex int

func GetDefaultInterfaceIndexDarwin() (int, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return 0, err
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp != 0 &&
			iface.Flags&net.FlagLoopback == 0 &&
			!strings.HasPrefix(iface.Name, "utun") {

			return iface.Index, nil
		}
	}

	return 0, fmt.Errorf("no active interface found")
}

// вызывается один раз
func SetDefaultInterfaceIndex(idx int) {
	defaultInterfaceIndex = idx
	log.Infof("[Darwin-Protect] Using interface index: %d", idx)
}

func protectSocket(fd uintptr, network string) {
	if defaultInterfaceIndex == 0 {
		log.Infof("[Darwin-Protect] interface index not set")
		return
	}

	var err error

	switch network {
	case "tcp4", "udp4":
		err = syscall.SetsockoptInt(
			int(fd),
			syscall.IPPROTO_IP,
			IP_BOUND_IF,
			defaultInterfaceIndex,
		)

	case "tcp6", "udp6":
		err = syscall.SetsockoptInt(
			int(fd),
			syscall.IPPROTO_IPV6,
			IPV6_BOUND_IF,
			defaultInterfaceIndex,
		)

	default:
		log.Infof("[Darwin-Protect] unsupported network: %s", network)
		return
	}

	if err != nil {
		log.Infof("[Darwin-Protect] setsockopt failed: %v", err)
	} else {
		log.Infof("[Darwin-Protect] setsockopt OK (ifindex=%d)", defaultInterfaceIndex)
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

func DialContextWithProtect(ctx context.Context, network, address string) (net.Conn, error) {
	realNet := normalizeTCP(address)

	d := &net.Dialer{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				protectSocket(fd, realNet)
			})
		},
	}

	return d.DialContext(ctx, realNet, address)
}

func DialUDPWithProtect(ctx context.Context, network, address string) (net.PacketConn, error) {
	realNet := normalizeUDP(address)

	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				protectSocket(fd, realNet)
			})
		},
	}

	addr := "0.0.0.0:0"
	if realNet == "udp6" {
		addr = "[::]:0"
	}

	pc, err := lc.ListenPacket(ctx, realNet, addr)
	if err != nil {
		return nil, err
	}

	udpAddr, err := net.ResolveUDPAddr(realNet, address)
	if err != nil {
		_ = pc.Close()
		return nil, err
	}

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
