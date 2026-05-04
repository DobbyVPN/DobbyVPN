package protected_dialer

import (
	"context"
	"fmt"
	"net"
	"strings"
	"syscall"

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

func protectSocket(fd uintptr, realNet string, destination string) error {
	if protector == nil {
		// iOS 26: Log warning if protector is nil - this could explain connectivity issues
		log.Infof("[Protect] WARNING: no socket protector registered - traffic may bypass VPN!")
		return fmt.Errorf("no socket protector registered")
	}

	if err := protector.Protect(fd, realNet); err != nil {
		// iOS 26: Log detailed error - socket protection failure may cause network issues
		log.Infof("[Protect] ERROR: %s fd=%d destination=%s protect_failed: %v - may cause iOS network issues", realNet, fd, destination, err)
		return err
	}

	// iOS 26: Log detailed success with more context
	log.Infof("[Protect] %s fd=%d destination=%s protect_ok", realNet, fd, destination)
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

func DialContextWithProtect(ctx context.Context, network, address string) (net.Conn, error) {
	realNet := normalizeTCP(address)

	if isLoopback(address) {
		log.Infof("[Protect] TCP BYPASS loopback: %s", address)
		var d net.Dialer
		return d.DialContext(ctx, realNet, address)
	}

	d := NewProtectedDialer(address)
	conn, err := d.DialContext(ctx, realNet, address)
	if err != nil {
		// iOS 26: Log detailed connection failure
		log.Infof("[Protect] TCP dial FAILED: dest=%s err=%v", address, err)
		return nil, err
	}

	// Protected upstream sockets must bypass the packet tunnel. If they use the
	// tunnel address, Outline/Cloak traffic can loop back into tun2socks.
	localAddr := conn.LocalAddr().String()
	log.Infof("[Protect] TCP dial OK: dest=%s local=%s remote=%s", address, localAddr, conn.RemoteAddr())

	if strings.HasPrefix(localAddr, "198.18.") {
		log.Infof("[Protect] *** CRITICAL *** Protected upstream TCP connection is using VPN tunnel address local=%s - routing loop risk", localAddr)
	}
	return conn, nil
}

func DialUDPWithProtect(ctx context.Context, network, address string) (net.PacketConn, error) {
	realNet := normalizeUDP(address)

	if isLoopback(address) {
		log.Infof("[Protect] UDP BYPASS loopback: %s", address)

		lc := net.ListenConfig{}

		pc, err := lc.ListenPacket(ctx, realNet, listenAddr(realNet))
		if err != nil {
			log.Infof("[Protect] UDP BYPASS loopback listen error network=%s destination=%s: %v", realNet, address, err)
			return nil, err
		}

		udpAddr, err := net.ResolveUDPAddr(realNet, address)
		if err != nil {
			_ = pc.Close()
			log.Infof("[Protect] UDP BYPASS loopback resolve error network=%s destination=%s: %v", realNet, address, err)
			return nil, err
		}

		log.Infof("[DEBUG][Protect] UDP BYPASS loopback ready network=%s destination=%s local=%s remote=%s", realNet, address, pc.LocalAddr(), udpAddr)
		return &connectedUDPConn{
			PacketConn: pc,
			remoteAddr: udpAddr,
		}, nil
	}

	lc := NewProtectedListenConfig(address)
	pc, err := lc.ListenPacket(ctx, realNet, listenAddr(realNet))
	if err != nil {
		log.Infof("[Protect] UDP listen error network=%s destination=%s: %v", realNet, address, err)
		return nil, err
	}

	udpAddr, err := net.ResolveUDPAddr(realNet, address)
	if err != nil {
		_ = pc.Close()
		log.Infof("[Protect] UDP resolve error network=%s destination=%s: %v", realNet, address, err)
		return nil, err
	}

	localAddr := pc.LocalAddr().String()
	log.Infof("[DEBUG][Protect] UDP dial ready network=%s destination=%s local=%s remote=%s", realNet, address, localAddr, udpAddr)

	if strings.HasPrefix(localAddr, "198.18.") {
		log.Infof("[Protect] *** CRITICAL *** Protected upstream UDP socket is using VPN tunnel address local=%s - routing loop risk", localAddr)
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
