package protected_dialer

import (
	"context"
	"fmt"
	"net"
	"syscall"
	"time"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	"github.com/xjasonlyu/tun2socks/v2/proxy"

	"go_module/dnscache"
	"go_module/log"
)

const (
	networkTCP4 = "tcp4"
	networkTCP6 = "tcp6"
	networkUDP4 = "udp4"
	networkUDP6 = "udp6"
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
		return networkTCP6
	}
	return networkTCP4
}

func normalizeUDP(address string) string {
	host, _, _ := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	if ip != nil && ip.To4() == nil {
		return networkUDP6
	}
	return networkUDP4
}

func listenAddr(network string) string {
	if network == networkUDP6 {
		return "[::]:0"
	}
	return "0.0.0.0:0"
}

func resolveAddressForProtect(ctx context.Context, address string) string {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return address
	}
	if ip := net.ParseIP(host); ip != nil {
		return address
	}

	ip, err := dnscache.ResolveIPv4(ctx, host, dnscache.FastResolveTimeout, "protected-dialer")
	if err != nil {
		log.Debugf(Category, "[Protect] DNS resolve skipped timeout=%s err=%v", dnscache.FastResolveTimeout, err)
		return address
	}

	resolved := net.JoinHostPort(ip.String(), port)
	log.Debugf(Category, "[Protect] DNS resolved destination_redacted=true")
	return resolved
}

// protectFD applies the platform's route-exclusion policy. It is deliberately
// strict: a non-loopback connection without successful protection is unsafe,
// because it can be sent back through the VPN tunnel it is trying to create.
func protectFD(fd uintptr, network, address string) error {
	if isLoopback(address) {
		return nil
	}
	if protector == nil {
		return ErrSocketProtectionUnavailable
	}

	log.Debugf(Category, "[Protect] protect_begin network=%s fd=%d destination_redacted=true protector=%T", network, fd, protector)
	if err := protector.Protect(fd, network); err != nil {
		return fmt.Errorf("%w: network=%s destination=%s: %w", ErrSocketProtectionUnavailable, network, address, err)
	}
	log.Debugf(Category, "[Protect] protect_end network=%s fd=%d destination=%s protector=%T", network, fd, address, protector)
	return nil
}

func protectRawConn(network, address string, c syscall.RawConn) error {
	var protectErr error
	err := c.Control(func(fd uintptr) {
		protectErr = protectFD(fd, network, address)
	})
	if err != nil {
		return err
	}
	return protectErr
}

func DialContextWithProtect(ctx context.Context, network, address string) (net.Conn, error) {
	start := time.Now()
	dialAddress := resolveAddressForProtect(ctx, address)
	realNet := normalizeTCP(dialAddress)
	if deadline, ok := ctx.Deadline(); ok {
		log.Debugf(Category, "[Protect] TCP dial begin requestedNetwork=%s realNetwork=%s destination_redacted=true deadline=%s protector=%T", network, realNet, deadline.Format(time.RFC3339Nano), protector)
	} else {
		log.Debugf(Category, "[Protect] TCP dial begin requestedNetwork=%s realNetwork=%s destination_redacted=true deadline=(none) protector=%T", network, realNet, protector)
	}

	if isLoopback(dialAddress) {
		log.Debugf(Category, "[Protect] TCP BYPASS loopback")
		var d net.Dialer
		conn, err := d.DialContext(ctx, realNet, dialAddress)
		if err != nil {
			log.Debugf(Category, "[Protect] TCP BYPASS loopback failed network=%s dest=%s dialDest=%s elapsed=%s err=%v", realNet, address, dialAddress, time.Since(start), err)
			return nil, err
		}
		log.Debugf(Category, "[Protect] TCP BYPASS loopback OK network=%s dest=%s dialDest=%s elapsed=%s local=%s remote=%s", realNet, address, dialAddress, time.Since(start), conn.LocalAddr(), conn.RemoteAddr())
		return conn, nil
	}

	d := &net.Dialer{
		Control: func(network, address string, c syscall.RawConn) error {
			err := protectRawConn(realNet, address, c)
			if err != nil {
				log.Debugf(Category, "[Protect] TCP protection failed network=%s dest=%s err=%v", realNet, address, err)
			}
			return err
		},
	}

	conn, err := d.DialContext(ctx, realNet, dialAddress)
	if err != nil {
		log.Debugf(Category, "[Protect] TCP dial FAILED dest=%s dialDest=%s elapsed=%s err=%v", address, dialAddress, time.Since(start), err)
		return nil, err
	}

	log.Debugf(Category, "[Protect] TCP dial OK dest=%s dialDest=%s elapsed=%s local=%s remote=%s", address, dialAddress, time.Since(start), conn.LocalAddr(), conn.RemoteAddr())
	return conn, nil
}

func DialUDPConnWithProtect(ctx context.Context, network, address string) (net.Conn, error) {
	start := time.Now()
	dialAddress := resolveAddressForProtect(ctx, address)
	realNet := normalizeUDP(dialAddress)
	if deadline, ok := ctx.Deadline(); ok {
		log.Debugf(Category, "[Protect] UDP conn dial begin requestedNetwork=%s realNetwork=%s dest=%s dialDest=%s deadline=%s protector=%T", network, realNet, address, dialAddress, deadline.Format(time.RFC3339Nano), protector)
	} else {
		log.Debugf(Category, "[Protect] UDP conn dial begin requestedNetwork=%s realNetwork=%s dest=%s dialDest=%s deadline=(none) protector=%T", network, realNet, address, dialAddress, protector)
	}

	d := net.Dialer{
		Control: ProtectRawConn,
	}
	conn, err := d.DialContext(ctx, realNet, dialAddress)
	if err != nil {
		log.Debugf(Category, "[Protect] UDP conn dial FAILED dest=%s dialDest=%s elapsed=%s err=%v", address, dialAddress, time.Since(start), err)
		return nil, err
	}
	log.Debugf(Category, "[Protect] UDP conn dial OK dest=%s dialDest=%s elapsed=%s local=%s remote=%s", address, dialAddress, time.Since(start), conn.LocalAddr(), conn.RemoteAddr())
	return conn, nil
}

func ProtectRawConn(network, address string, c syscall.RawConn) error {
	realNet := network
	if realNet == "tcp" || realNet == "" {
		realNet = normalizeTCP(address)
	}

	return protectRawConn(realNet, address, c)
}

func DialUDPWithProtect(ctx context.Context, network, address string) (net.PacketConn, error) {
	start := time.Now()
	dialAddress := resolveAddressForProtect(ctx, address)
	realNet := normalizeUDP(dialAddress)
	if deadline, ok := ctx.Deadline(); ok {
		log.Debugf(Category, "[Protect] UDP dial begin requestedNetwork=%s realNetwork=%s dest=%s dialDest=%s deadline=%s protector=%T", network, realNet, address, dialAddress, deadline.Format(time.RFC3339Nano), protector)
	} else {
		log.Debugf(Category, "[Protect] UDP dial begin requestedNetwork=%s realNetwork=%s dest=%s dialDest=%s deadline=(none) protector=%T", network, realNet, address, dialAddress, protector)
	}

	if isLoopback(dialAddress) {
		log.Debugf(Category, "[Protect] UDP BYPASS loopback: %s", dialAddress)

		lc := net.ListenConfig{}

		pc, err := lc.ListenPacket(ctx, realNet, listenAddr(realNet))
		if err != nil {
			log.Debugf(Category, "[Protect] UDP BYPASS loopback listen error network=%s destination=%s dialDest=%s elapsed=%s err=%v", realNet, address, dialAddress, time.Since(start), err)
			return nil, err
		}

		udpAddr, err := net.ResolveUDPAddr(realNet, dialAddress)
		if err != nil {
			if closeErr := pc.Close(); closeErr != nil {
				log.Debugf(Category, "[Protect] UDP BYPASS loopback close after resolve error failed network=%s destination=%s closeErr=%v", realNet, address, closeErr)
			}
			log.Debugf(Category, "[Protect] UDP BYPASS loopback resolve error network=%s destination=%s dialDest=%s elapsed=%s err=%v", realNet, address, dialAddress, time.Since(start), err)
			return nil, err
		}

		log.Debugf(Category, "[Protect] UDP BYPASS loopback OK network=%s destination=%s dialDest=%s elapsed=%s local=%s remote=%s", realNet, address, dialAddress, time.Since(start), pc.LocalAddr(), udpAddr)
		return &connectedUDPConn{
			PacketConn: pc,
			remoteAddr: udpAddr,
		}, nil
	}

	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			err := protectRawConn(realNet, address, c)
			if err != nil {
				log.Debugf(Category, "[Protect] UDP protection failed network=%s dest=%s err=%v", realNet, address, err)
			}
			return err
		},
	}

	pc, err := lc.ListenPacket(ctx, realNet, listenAddr(realNet))
	if err != nil {
		log.Debugf(Category, "[Protect] UDP listen error network=%s destination=%s elapsed=%s err=%v", realNet, address, time.Since(start), err)
		return nil, err
	}

	udpAddr, err := net.ResolveUDPAddr(realNet, dialAddress)
	if err != nil {
		if closeErr := pc.Close(); closeErr != nil {
			log.Debugf(Category, "[Protect] UDP close after resolve error failed network=%s destination=%s closeErr=%v", realNet, address, closeErr)
		}
		log.Debugf(Category, "[Protect] UDP resolve error network=%s destination=%s dialDest=%s elapsed=%s err=%v", realNet, address, dialAddress, time.Since(start), err)
		return nil, err
	}

	log.Debugf(Category, "[Protect] UDP dial OK network=%s destination=%s dialDest=%s elapsed=%s local=%s remote=%s", realNet, address, dialAddress, time.Since(start), pc.LocalAddr(), udpAddr)
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

// ProtectSocketInt allows external modules (like C/C++ integrations) to apply socket protection
// natively using DobbyVPN's cross-platform socket protector.
// ProtectSocketIntErr is for integrations whose callback can signal an error
// to their transport. Such callers must propagate this error instead of
// claiming the socket was protected.
func ProtectSocketIntErr(fd int) error {
	if protector == nil {
		return ErrSocketProtectionUnavailable
	}
	return protectFD(uintptr(fd), networkTCP4, "external-socket")
}

// ProtectSocketInt remains source-compatible with existing native bindings.
// New bindings should use ProtectSocketIntErr so failures are observable.
func ProtectSocketInt(fd int) bool {
	err := ProtectSocketIntErr(fd)
	if err != nil {
		log.Infof("protected_dialer", "[Protect] ProtectSocketInt failed fd=%d err=%v", fd, err)
		return false
	}
	return true
}
