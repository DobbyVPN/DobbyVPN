//go:build windows && !(android || ios)

package protected_dialer

import (
	"context"
	"net"
	"syscall"

	"github.com/jackpal/gateway"
	"go_client/log"
	"go_client/routing"
)

var defaultInterfaceIndex int

func GetDefaultInterfaceIndex() (int, error) {
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		return 0, err
	}

	ifaceIP, err := routing.FindInterfaceIPByGateway(gatewayIP.String())
	if err != nil {
		return 0, err
	}

	iface, err := routing.GetNetworkInterfaceByIP(ifaceIP)
	if err != nil {
		return 0, err
	}

	return iface.Index, nil
}

func SetDefaultInterfaceIndex(idx int) {
	defaultInterfaceIndex = idx
	log.Infof("[Windows-Protect] Using interface index: %d", idx)
}

func htonl(i uint32) uint32 {
	return (i&0xff)<<24 | (i&0xff00)<<8 | (i&0xff0000)>>8 | (i&0xff000000)>>24
}

func protectSocket(fd uintptr, network string) {
	if defaultInterfaceIndex == 0 {
		log.Infof("[Windows-Protect] interface index not set")
		return
	}

	var err error

	switch network {
	case "tcp4", "udp4":
		const IP_UNICAST_IF = 31
		idx := int(htonl(uint32(defaultInterfaceIndex)))
		err = syscall.SetsockoptInt(
			syscall.Handle(fd),
			syscall.IPPROTO_IP,
			IP_UNICAST_IF,
			idx,
		)

	case "tcp6", "udp6":
		const IPV6_UNICAST_IF = 31
		err = syscall.SetsockoptInt(
			syscall.Handle(fd),
			syscall.IPPROTO_IPV6,
			IPV6_UNICAST_IF,
			defaultInterfaceIndex,
		)

	default:
		log.Infof("[Windows-Protect] unsupported network for protect: %s", network)
		return
	}

	if err != nil {
		log.Infof("[Windows-Protect] setsockopt failed for %s: %v", network, err)
	} else {
		log.Infof("[Windows-Protect] setsockopt ok for %s via ifindex=%d", network, defaultInterfaceIndex)
	}
}

func normalizeTCPNetwork(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return "tcp4"
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return "tcp4"
	}

	if ip.To4() != nil {
		return "tcp4"
	}
	return "tcp6"
}

func normalizeUDPNetwork(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return "udp4"
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return "udp4"
	}

	if ip.To4() != nil {
		return "udp4"
	}
	return "udp6"
}

func DialContextWithProtect(ctx context.Context, network string, address string) (net.Conn, error) {
	realNetwork := normalizeTCPNetwork(address)

	d := &net.Dialer{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				protectSocket(fd, realNetwork)
			})
		},
	}

	return d.DialContext(ctx, realNetwork, address)
}

func DialUDPWithProtect(ctx context.Context, network string, address string) (net.PacketConn, error) {
	realNetwork := normalizeUDPNetwork(address)

	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				protectSocket(fd, realNetwork)
			})
		},
	}

	listenAddr := "0.0.0.0:0"
	if realNetwork == "udp6" {
		listenAddr = "[::]:0"
	}

	pc, err := lc.ListenPacket(ctx, realNetwork, listenAddr)
	if err != nil {
		return nil, err
	}

	udpAddr, err := net.ResolveUDPAddr(realNetwork, address)
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
