//go:build darwin
// +build darwin

package tunnel

import (
	"context"
	"fmt"
	"github.com/jackpal/gateway"
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

// --- INIT ---

func isReachableViaInterface(iface net.Interface, gw net.IP) bool {
	addrs, _ := iface.Addrs()

	for _, addr := range addrs {
		ip, ipnet, _ := net.ParseCIDR(addr.String())
		if ip == nil || ip.To4() == nil {
			continue
		}

		if ipnet.Contains(gw) {
			return true
		}
	}

	return false
}

func GetDefaultInterfaceNameDarwin() (string, int, error) {
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		return "", 0, err
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return "", 0, err
	}

	for _, iface := range ifaces {

		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if len(iface.HardwareAddr) == 0 {
			continue
		}

		if strings.HasPrefix(iface.Name, "utun") ||
			strings.HasPrefix(iface.Name, "awdl") ||
			strings.HasPrefix(iface.Name, "llw") ||
			strings.HasPrefix(iface.Name, "bridge") ||
			strings.HasPrefix(iface.Name, "lo") {
			continue
		}

		if isReachableViaInterface(iface, gatewayIP) {
			log.Infof("[Darwin-Protect] Selected REAL iface=%s index=%d", iface.Name, iface.Index)
			return iface.Name, iface.Index, nil
		}
	}

	return "", 0, fmt.Errorf("no interface for gateway found")
}

func SetDefaultInterface(idx int) {
	defaultInterfaceIndex = idx
	log.Infof("[Darwin-Protect] Using interface index=%d", idx)
}

// --- SOCKET PROTECT ---

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

// --- HELPERS ---

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

// --- TCP ---

func DialContextWithProtect(ctx context.Context, network, address string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(address)
	if err == nil {
		ip := net.ParseIP(host)
		if ip != nil && ip.IsLoopback() {
			log.Infof("[Darwin-Protect] skip loopback %s", address)
			var d net.Dialer
			return d.DialContext(ctx, normalizeTCP(address), address)
		}
	}

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

// --- UDP ---

func DialUDPWithProtect(ctx context.Context, network, address string) (net.PacketConn, error) {
	host, _, err := net.SplitHostPort(address)
	if err == nil {
		ip := net.ParseIP(host)
		if ip != nil && ip.IsLoopback() {
			log.Infof("[Darwin-Protect] skip loopback UDP %s", address)

			realNet := normalizeUDP(address)
			pc, err := net.ListenPacket(realNet, "0.0.0.0:0")
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
	}

	realNet := normalizeUDP(address)

	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				protectSocket(fd, realNet)
			})
		},
	}

	listenAddr := "0.0.0.0:0"
	if realNet == "udp6" {
		listenAddr = "[::]:0"
	}

	pc, err := lc.ListenPacket(ctx, realNet, listenAddr)
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

// --- CONNECTED UDP ---

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
