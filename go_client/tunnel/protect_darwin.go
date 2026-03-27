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

// --- INIT ---

func isReachableViaInterface(iface net.Interface, gw net.IP) bool {
	addrs, _ := iface.Addrs()

	for _, addr := range addrs {
		ip, ipnet, _ := net.ParseCIDR(addr.String())
		if ip == nil || ip.To4() == nil {
			continue
		}

		if ipnet.Contains(gw) {
			log.Infof("[Darwin-Protect][Detect] iface=%s contains gateway %s (cidr=%s)", iface.Name, gw.String(), ipnet.String())
			return true
		}
	}

	return false
}

func GetDefaultInterfaceNameDarwin(gatewayIP net.IP) (string, int, error) {
	log.Infof("[Darwin-Protect][Detect] Gateway detected: %s", gatewayIP.String())

	ifaces, err := net.Interfaces()
	if err != nil {
		return "", 0, err
	}

	for _, iface := range ifaces {

		log.Infof("[Darwin-Protect][Detect] Checking iface=%s flags=%v", iface.Name, iface.Flags)

		if iface.Flags&net.FlagUp == 0 {
			log.Infof("[Darwin-Protect][Detect] skip %s: down", iface.Name)
			continue
		}
		if iface.Flags&net.FlagLoopback != 0 {
			log.Infof("[Darwin-Protect][Detect] skip %s: loopback", iface.Name)
			continue
		}
		if len(iface.HardwareAddr) == 0 {
			log.Infof("[Darwin-Protect][Detect] skip %s: no MAC", iface.Name)
			continue
		}

		if strings.HasPrefix(iface.Name, "utun") ||
			strings.HasPrefix(iface.Name, "awdl") ||
			strings.HasPrefix(iface.Name, "llw") ||
			strings.HasPrefix(iface.Name, "bridge") ||
			strings.HasPrefix(iface.Name, "lo") {
			log.Infof("[Darwin-Protect][Detect] skip %s: virtual/unsupported", iface.Name)
			continue
		}

		if isReachableViaInterface(iface, gatewayIP) {
			log.Infof("[Darwin-Protect][Detect] SELECTED iface=%s index=%d (gateway reachable)", iface.Name, iface.Index)
			return iface.Name, iface.Index, nil
		}
	}

	return "", 0, fmt.Errorf("no interface for gateway found")
}

func SetDefaultInterface(idx int) {
	defaultInterfaceIndex = idx
	log.Infof("[Darwin-Protect][State] Using interface index=%d for protected sockets", idx)
}

// --- SOCKET PROTECT ---

func protectSocket(fd uintptr, network string) {
	if defaultInterfaceIndex == 0 {
		log.Infof("[Darwin-Protect][Protect] SKIP: interface index not set")
		return
	}

	var err error

	log.Infof("[Darwin-Protect][Protect] Applying socket protect fd=%d net=%s ifindex=%d", fd, network, defaultInterfaceIndex)

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
		log.Infof("[Darwin-Protect][Protect] SKIP: unsupported network=%s", network)
		return
	}

	if err != nil {
		log.Infof("[Darwin-Protect][Protect] ERROR: setsockopt failed: %v", err)
	} else {
		log.Infof("[Darwin-Protect][Protect] OK: socket bound to ifindex=%d", defaultInterfaceIndex)
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
	log.Infof("[Darwin-Protect][TCP] Dial request: %s %s", network, address)

	host, _, err := net.SplitHostPort(address)
	if err == nil {
		ip := net.ParseIP(host)
		if ip != nil && ip.IsLoopback() {
			log.Infof("[Darwin-Protect][TCP] BYPASS: loopback %s", address)
			var d net.Dialer
			return d.DialContext(ctx, normalizeTCP(address), address)
		}
	}

	realNet := normalizeTCP(address)
	log.Infof("[Darwin-Protect][TCP] Normalized network: %s", realNet)

	d := &net.Dialer{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				protectSocket(fd, realNet)
			})
		},
	}

	conn, err := d.DialContext(ctx, realNet, address)
	if err != nil {
		log.Infof("[Darwin-Protect][TCP] ERROR: dial failed: %v", err)
		return nil, err
	}

	log.Infof("[Darwin-Protect][TCP] OK: connected to %s via protected socket", address)
	return conn, nil
}

// --- UDP ---

func DialUDPWithProtect(ctx context.Context, network, address string) (net.PacketConn, error) {
	log.Infof("[Darwin-Protect][UDP] Dial request: %s %s", network, address)

	host, _, err := net.SplitHostPort(address)
	if err == nil {
		ip := net.ParseIP(host)
		if ip != nil && ip.IsLoopback() {
			log.Infof("[Darwin-Protect][UDP] BYPASS: loopback %s", address)

			realNet := normalizeUDP(address)
			pc, err := net.ListenPacket(realNet, "0.0.0.0:0")
			if err != nil {
				log.Infof("[Darwin-Protect][UDP] ERROR: listen failed: %v", err)
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
	log.Infof("[Darwin-Protect][UDP] Normalized network: %s", realNet)

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
		log.Infof("[Darwin-Protect][UDP] ERROR: ListenPacket failed: %v", err)
		return nil, err
	}

	udpAddr, err := net.ResolveUDPAddr(realNet, address)
	if err != nil {
		_ = pc.Close()
		return nil, err
	}

	log.Infof("[Darwin-Protect][UDP] OK: socket ready for %s", address)

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
