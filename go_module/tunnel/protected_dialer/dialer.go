package protected_dialer

import (
	"context"
	"fmt"
	"net"
	"strings"
	"syscall"
	"time"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	"github.com/xjasonlyu/tun2socks/v2/proxy"

	"go_module/log"
)

type ProtectedDirectProxy struct {
	proxy.Proxy
}

func isLoopback(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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

func listenAddr(network string) string {
	if network == "udp6" {
		return "[::]:0"
	}
	return "0.0.0.0:0"
}

func protectSocket(fd uintptr, realNet, destination string) error {
	if protector == nil {
		// iOS 26: Log warning if protector is nil - this could explain connectivity issues
		log.Debugf(Category, "[Protect] WARNING: no socket protector registered network=%s destination=%s fd=%d - traffic may bypass VPN!", realNet, destination, fd)
		return fmt.Errorf("no socket protector registered")
	}

	log.Debugf(Category, "[Protect] protect_begin network=%s fd=%d destination=%s", realNet, fd, destination)
	if err := protector.Protect(fd, realNet); err != nil {
		// iOS 26: Log detailed error - socket protection failure may cause network issues
		log.Debugf(Category, "[Protect] ERROR: %s fd=%d destination=%s protect_failed: %v - may cause iOS network issues", realNet, fd, destination, err)
		return err
	}

	// iOS 26: Log detailed success with more context
	log.Debugf(Category, "[Protect] %s fd=%d destination=%s protect_ok", realNet, fd, destination)
	return nil
}

// NewProtectedDialer returns a net.Dialer that protects its sockets.
func NewProtectedDialer(destination string) *net.Dialer {
	return &net.Dialer{
		Control: func(network, address string, c syscall.RawConn) error {
			var protectErr error
			controlErr := c.Control(func(fd uintptr) {
				protectErr = protectSocket(fd, network, destination)
			})
			if controlErr != nil {
				return controlErr
			}
			return protectErr
		},
	}
}

// NewProtectedListenConfig returns a net.ListenConfig that protects its sockets.
func NewProtectedListenConfig(destination string) *net.ListenConfig {
	return &net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var protectErr error
			controlErr := c.Control(func(fd uintptr) {
				protectErr = protectSocket(fd, network, destination)
			})
			if controlErr != nil {
				return controlErr
			}
			return protectErr
		},
	}
}

type diagnosticProtector interface {
	Diagnostics() string
}

func protectionDiagnosticsForLog() string {
	if protector == nil {
		return "protector=nil"
	}
	if diagnostic, ok := protector.(diagnosticProtector); ok {
		return diagnostic.Diagnostics()
	}
	return fmt.Sprintf("protector=%T diagnostics=unavailable", protector)
}

func splitAddrHost(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	return host
}

func destinationPartsForLog(address string) (host string, port string, hostType string) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return address, "", "invalid_hostport"
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() != nil {
			return host, port, "ipv4"
		}
		return host, port, "ipv6"
	}
	return host, port, "hostname"
}

func interfaceNamesForIP(host string) string {
	ip := net.ParseIP(host)
	if ip == nil {
		return "ip_parse_failed"
	}

	interfaces, err := net.Interfaces()
	if err != nil {
		return fmt.Sprintf("scan_error=%v", err)
	}

	var matches []string
	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			matches = append(matches, fmt.Sprintf("%s(index=%d addr_scan_error=%v)", iface.Name, iface.Index, err))
			continue
		}
		for _, addr := range addrs {
			var addrIP net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				addrIP = v.IP
			case *net.IPAddr:
				addrIP = v.IP
			default:
				continue
			}
			if addrIP.Equal(ip) {
				matches = append(matches, fmt.Sprintf("%s(index=%d flags=%s mtu=%d)", iface.Name, iface.Index, iface.Flags.String(), iface.MTU))
			}
		}
	}

	if len(matches) == 0 {
		return "none"
	}
	return strings.Join(matches, ";")
}

func isTunnelSourceHost(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	ip4 := ip.To4()
	return ip4 != nil && ip4[0] == 198 && (ip4[1] == 18 || ip4[1] == 19)
}

func isUnspecifiedSourceHost(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

func validateProtectedAddrs(kind string, localAddr net.Addr, remoteAddr net.Addr, requestedNetwork string, realNetwork string, address string, started time.Time) error {
	localHost := splitAddrHost(localAddr)
	remoteHost := splitAddrHost(remoteAddr)
	destHost, destPort, hostType := destinationPartsForLog(address)
	localInterfaces := interfaceNamesForIP(localHost)
	remoteInterfaces := interfaceNamesForIP(remoteHost)
	protectionDiagnostics := protectionDiagnosticsForLog()
	elapsedMs := time.Since(started).Milliseconds()

	verdict := "BYPASS_OK"
	if isTunnelSourceHost(localHost) {
		verdict = "BYPASS_FAILED_TUNNEL_SOURCE"
	} else if isUnspecifiedSourceHost(localHost) {
		verdict = "BYPASS_SOURCE_UNSPECIFIED"
	}

	log.Infof(
		"[Protect] %s effective_bypass verdict=%s requestedNetwork=%s realNetwork=%s dest=%s destHost=%s destPort=%s destHostType=%s resolvedRemote=%s local=%s localHost=%s localInterfaces=[%s] remote=%s remoteHost=%s remoteInterfaces=[%s] elapsedMs=%d protection={%s}",
		kind,
		verdict,
		requestedNetwork,
		realNetwork,
		address,
		destHost,
		destPort,
		hostType,
		remoteHost,
		localAddr,
		localHost,
		localInterfaces,
		remoteAddr,
		remoteHost,
		remoteInterfaces,
		elapsedMs,
		protectionDiagnostics,
	)

	if verdict == "BYPASS_FAILED_TUNNEL_SOURCE" {
		err := fmt.Errorf("protected %s upstream routed through tunnel source local=%s remote=%s dest=%s", strings.ToLower(kind), localAddr, remoteAddr, address)
		log.Debugf(Category, "[Protect] %s effective_bypass hard_fail err=%v", kind, err)
		return err
	}
	return nil
}

func validateProtectedConn(kind string, conn net.Conn, requestedNetwork string, realNetwork string, address string, started time.Time) error {
	return validateProtectedAddrs(kind, conn.LocalAddr(), conn.RemoteAddr(), requestedNetwork, realNetwork, address, started)
}

func DialContextWithProtect(ctx context.Context, network, address string) (net.Conn, error) {
	realNet := normalizeTCP(address)
	start := time.Now()
	if deadline, ok := ctx.Deadline(); ok {
		log.Debugf(Category, "[Protect] TCP dial begin requestedNetwork=%s realNetwork=%s dest=%s deadline=%s protection={%s}", network, realNet, address, deadline.Format("2006-01-02T15:04:05.000Z07:00"), protectionDiagnosticsForLog())
	} else {
		log.Debugf(Category, "[Protect] TCP dial begin requestedNetwork=%s realNetwork=%s dest=%s deadline=(none) protection={%s}", network, realNet, address, protectionDiagnosticsForLog())
	}

	if isLoopback(address) {
		log.Debugf(Category, "[Protect] TCP BYPASS loopback: %s", address)
		var d net.Dialer
		conn, err := d.DialContext(ctx, realNet, address)
		if err != nil {
			log.Debugf(Category, "[Protect] TCP BYPASS loopback failed network=%s dest=%s err=%v", realNet, address, err)
			return nil, err
		}
		log.Debugf(Category, "[Protect] TCP BYPASS loopback OK network=%s dest=%s local=%s remote=%s", realNet, address, conn.LocalAddr(), conn.RemoteAddr())
		return conn, nil
	}

	d := NewProtectedDialer(address)
	conn, err := d.DialContext(ctx, realNet, address)
	if err != nil {
		// iOS 26: Log detailed connection failure
		log.Debugf(Category, "[Protect] TCP dial FAILED: dest=%s err=%v", address, err)
		return nil, err
	}

	// Protected upstream sockets must bypass the packet tunnel. If they use the
	// tunnel address, Outline/Cloak traffic can loop back into tun2socks.
	log.Debugf(Category, "[Protect] TCP dial OK: dest=%s local=%s remote=%s", address, conn.LocalAddr(), conn.RemoteAddr())

	if err := validateProtectedConn("TCP", conn, network, realNet, address, start); err != nil {
		if closeErr := conn.Close(); closeErr != nil {
			log.Debugf(Category, "[Protect] TCP close after bypass validation failure failed dest=%s closeErr=%v", address, closeErr)
		}
		return nil, err
	}
	return conn, nil
}

func DialPacketWithProtect(ctx context.Context, network, address string) (net.Conn, error) {
	realNet := normalizeUDP(address)
	start := time.Now()
	if deadline, ok := ctx.Deadline(); ok {
		log.Debugf(Category, "[Protect] UDP conn dial begin requestedNetwork=%s realNetwork=%s dest=%s deadline=%s protection={%s}", network, realNet, address, deadline.Format("2006-01-02T15:04:05.000Z07:00"), protectionDiagnosticsForLog())
	} else {
		log.Debugf(Category, "[Protect] UDP conn dial begin requestedNetwork=%s realNetwork=%s dest=%s deadline=(none) protection={%s}", network, realNet, address, protectionDiagnosticsForLog())
	}

	if isLoopback(address) {
		log.Debugf(Category, "[Protect] UDP conn BYPASS loopback: %s", address)
		var d net.Dialer
		conn, err := d.DialContext(ctx, realNet, address)
		if err != nil {
			log.Debugf(Category, "[Protect] UDP conn BYPASS loopback failed network=%s dest=%s err=%v", realNet, address, err)
			return nil, err
		}
		log.Debugf(Category, "[Protect] UDP conn BYPASS loopback OK network=%s dest=%s local=%s remote=%s", realNet, address, conn.LocalAddr(), conn.RemoteAddr())
		return conn, nil
	}

	d := NewProtectedDialer(address)
	conn, err := d.DialContext(ctx, realNet, address)
	if err != nil {
		log.Debugf(Category, "[Protect] UDP conn dial FAILED: dest=%s err=%v", address, err)
		return nil, err
	}
	log.Debugf(Category, "[Protect] UDP conn dial OK: dest=%s local=%s remote=%s", address, conn.LocalAddr(), conn.RemoteAddr())

	if err := validateProtectedConn("UDP", conn, network, realNet, address, start); err != nil {
		if closeErr := conn.Close(); closeErr != nil {
			log.Debugf(Category, "[Protect] UDP conn close after bypass validation failure failed dest=%s closeErr=%v", address, closeErr)
		}
		return nil, err
	}
	return conn, nil
}

func DialUDPWithProtect(ctx context.Context, network, address string) (net.PacketConn, error) {
	realNet := normalizeUDP(address)
	start := time.Now()
	if deadline, ok := ctx.Deadline(); ok {
		log.Debugf(Category, "[Protect] UDP dial begin requestedNetwork=%s realNetwork=%s dest=%s deadline=%s protection={%s}", network, realNet, address, deadline.Format("2006-01-02T15:04:05.000Z07:00"), protectionDiagnosticsForLog())
	} else {
		log.Debugf(Category, "[Protect] UDP dial begin requestedNetwork=%s realNetwork=%s dest=%s deadline=(none) protection={%s}", network, realNet, address, protectionDiagnosticsForLog())
	}

	if isLoopback(address) {
		log.Debugf(Category, "[Protect] UDP BYPASS loopback: %s", address)

		lc := net.ListenConfig{}

		pc, err := lc.ListenPacket(ctx, realNet, listenAddr(realNet))
		if err != nil {
			log.Debugf(Category, "[Protect] UDP BYPASS loopback listen error network=%s destination=%s: %v", realNet, address, err)
			return nil, err
		}

		udpAddr, err := net.ResolveUDPAddr(realNet, address)
		if err != nil {
			if closeErr := pc.Close(); closeErr != nil {
				log.Debugf(Category, "[Protect] UDP BYPASS loopback close after resolve error failed network=%s destination=%s closeErr=%v", realNet, address, closeErr)
			}
			log.Debugf(Category, "[Protect] UDP BYPASS loopback resolve error network=%s destination=%s: %v", realNet, address, err)
			return nil, err
		}

		log.Debugf(Category, "[DEBUG][Protect] UDP BYPASS loopback ready network=%s destination=%s local=%s remote=%s", realNet, address, pc.LocalAddr(), udpAddr)
		return &connectedUDPConn{
			PacketConn: pc,
			remoteAddr: udpAddr,
		}, nil
	}

	lc := NewProtectedListenConfig(address)
	pc, err := lc.ListenPacket(ctx, realNet, listenAddr(realNet))
	if err != nil {
		log.Debugf(Category, "[Protect] UDP listen error network=%s destination=%s: %v", realNet, address, err)
		return nil, err
	}

	udpAddr, err := net.ResolveUDPAddr(realNet, address)
	if err != nil {
		if closeErr := pc.Close(); closeErr != nil {
			log.Debugf(Category, "[Protect] UDP close after resolve error failed network=%s destination=%s closeErr=%v", realNet, address, closeErr)
		}
		log.Debugf(Category, "[Protect] UDP resolve error network=%s destination=%s: %v", realNet, address, err)
		return nil, err
	}

	localAddr := pc.LocalAddr().String()
	log.Debugf(Category, "[DEBUG][Protect] UDP dial ready network=%s destination=%s local=%s remote=%s", realNet, address, localAddr, udpAddr)

	if err := validateProtectedAddrs("UDP_PACKET", pc.LocalAddr(), udpAddr, network, realNet, address, start); err != nil {
		if closeErr := pc.Close(); closeErr != nil {
			log.Debugf(Category, "[Protect] UDP close after bypass validation failure failed dest=%s closeErr=%v", address, closeErr)
		}
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

func (p *ProtectedDirectProxy) DialContext(ctx context.Context, metadata *M.Metadata) (net.Conn, error) {
	return DialContextWithProtect(ctx, metadata.Network.String(), metadata.DestinationAddress())
}

func (p *ProtectedDirectProxy) DialUDP(metadata *M.Metadata) (net.PacketConn, error) {
	return DialUDPWithProtect(context.Background(), metadata.Network.String(), metadata.DestinationAddress())
}
