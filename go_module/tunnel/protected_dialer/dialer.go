package protected_dialer

import (
	"context"
	"fmt"
	"net"
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
		log.Infof("[Protect] ERROR: %s fd=%d destination=%s failed: %v - THIS MAY CAUSE CONNECTIVITY ISSUES ON iOS 26", realNet, fd, destination, err)
		return err
	}
	log.Infof("[Protect] %s fd=%d destination=%s OK", realNet, fd, destination)
	return nil
}

func DialContextWithProtect(ctx context.Context, network, address string) (net.Conn, error) {
	realNet := normalizeTCP(address)

	if isLoopback(address) {
		log.Infof("[Protect] TCP BYPASS loopback: %s", address)
		var d net.Dialer
		return d.DialContext(ctx, realNet, address)
	}

	d := &net.Dialer{
		Control: func(_, _ string, c syscall.RawConn) error {
			var protectErr error
			controlErr := c.Control(func(fd uintptr) {
				protectErr = protectSocket(fd, realNet, address)
			})
			if controlErr != nil {
				return controlErr
			}
			return protectErr
		},
	}

	conn, err := d.DialContext(ctx, realNet, address)
	if err != nil {
		log.Infof("[Protect] TCP dial error: %v", err)
		return nil, err
	}

	log.Infof("[DEBUG][Protect] TCP dial OK network=%s destination=%s local=%s remote=%s", realNet, address, conn.LocalAddr(), conn.RemoteAddr())
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

	lc := net.ListenConfig{
		Control: func(_, _ string, c syscall.RawConn) error {
			var protectErr error
			controlErr := c.Control(func(fd uintptr) {
				protectErr = protectSocket(fd, realNet, address)
			})
			if controlErr != nil {
				return controlErr
			}
			return protectErr
		},
	}

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

	log.Infof("[DEBUG][Protect] UDP dial ready network=%s destination=%s local=%s remote=%s", realNet, address, pc.LocalAddr(), udpAddr)
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
