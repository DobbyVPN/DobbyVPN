package internal

import (
	"context"
	"fmt"
	"net"
	"syscall"
	"time"

	"go_module/tunnel/protected_dialer"

	xrayerrors "github.com/xtls/xray-core/common/errors"
	xraynet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/transport/internet"
)

type socketProtector func(network, destination string, rawConn syscall.RawConn) error
type outboundSocketOptions func(network, address string, fd uintptr, config *internet.SocketConfig) error

// protectedSystemDialer mirrors Xray's default system dialer while making the
// platform socket-protection result fatal. Xray's dialer-controller API logs
// and ignores controller errors, which can route the connection being
// protected back into the VPN tunnel.
type protectedSystemDialer struct {
	protect      socketProtector
	applyOptions outboundSocketOptions
}

func newProtectedSystemDialer(protect socketProtector, applyOptions outboundSocketOptions) *protectedSystemDialer {
	return &protectedSystemDialer{protect: protect, applyOptions: applyOptions}
}

func newAndroidProtectedSystemDialer() *protectedSystemDialer {
	return newProtectedSystemDialer(protected_dialer.ProtectRawConn, applyPlatformOutboundSocketOptions)
}

func (d *protectedSystemDialer) DestIpAddress() net.IP { return nil }

func (d *protectedSystemDialer) Dial(ctx context.Context, source xraynet.Address, destination xraynet.Destination, sockopt *internet.SocketConfig) (net.Conn, error) {
	address := destination.NetAddr()
	switch destination.Network {
	case xraynet.Network_TCP:
		return d.dialStream(ctx, source, destination, address, sockopt)
	case xraynet.Network_UDP:
		return d.dialPacket(ctx, source, destination, address, sockopt)
	default:
		return nil, fmt.Errorf("protected Android Xray dialer does not support network %q", destination.Network)
	}
}

func (d *protectedSystemDialer) dialStream(ctx context.Context, source xraynet.Address, destination xraynet.Destination, address string, sockopt *internet.SocketConfig) (net.Conn, error) {
	keepAliveConfig := net.KeepAliveConfig{
		Enable:   true,
		Idle:     45 * time.Second,
		Interval: 45 * time.Second,
		Count:    -1,
	}
	keepAlive := time.Duration(0)
	if sockopt != nil {
		if sockopt.TcpKeepAliveIdle*sockopt.TcpKeepAliveInterval < 0 {
			return nil, fmt.Errorf("invalid TcpKeepAliveIdle or TcpKeepAliveInterval value: %d %d", sockopt.TcpKeepAliveIdle, sockopt.TcpKeepAliveInterval)
		}
		if sockopt.TcpKeepAliveIdle < 0 || sockopt.TcpKeepAliveInterval < 0 {
			keepAlive = -1
			keepAliveConfig.Enable = false
		}
		if sockopt.TcpKeepAliveIdle > 0 {
			keepAliveConfig.Idle = time.Duration(sockopt.TcpKeepAliveIdle) * time.Second
		}
		if sockopt.TcpKeepAliveInterval > 0 {
			keepAliveConfig.Interval = time.Duration(sockopt.TcpKeepAliveInterval) * time.Second
		}
	}

	dialer := &net.Dialer{
		Timeout:         16 * time.Second,
		LocalAddr:       sourceAddr(destination.Network, source),
		KeepAlive:       keepAlive,
		KeepAliveConfig: keepAliveConfig,
		Control:         d.control(ctx, destination, sockopt, false),
	}
	if sockopt != nil && sockopt.TcpMptcp {
		dialer.SetMultipathTCP(true)
	}
	return dialer.DialContext(ctx, destination.Network.SystemString(), address)
}

func (d *protectedSystemDialer) dialPacket(ctx context.Context, source xraynet.Address, destination xraynet.Destination, address string, sockopt *internet.SocketConfig) (net.Conn, error) {
	if !hasBindAddress(sockopt) {
		local := sourceAddr(xraynet.Network_UDP, source)
		if local == nil {
			local = &net.UDPAddr{IP: net.IPv4zero, Port: 0}
		}
		destinationAddr, err := net.ResolveUDPAddr("udp", address)
		if err != nil {
			return nil, err
		}
		listenConfig := net.ListenConfig{
			Control: d.control(ctx, destination, sockopt, false),
		}
		packetConn, err := listenConfig.ListenPacket(ctx, local.Network(), local.String())
		if err != nil {
			return nil, err
		}
		return &internet.PacketConnWrapper{PacketConn: packetConn, Dest: destinationAddr}, nil
	}

	dialer := &net.Dialer{
		Timeout:   16 * time.Second,
		LocalAddr: sourceAddr(xraynet.Network_UDP, source),
		Control:   d.control(ctx, destination, sockopt, true),
	}
	if sockopt.TcpMptcp {
		dialer.SetMultipathTCP(true)
	}
	return dialer.DialContext(ctx, destination.Network.SystemString(), address)
}

func sourceAddr(network xraynet.Network, source xraynet.Address) net.Addr {
	if source == nil || source == xraynet.AnyIP {
		return nil
	}
	if network == xraynet.Network_TCP {
		return &net.TCPAddr{IP: source.IP(), Port: 0}
	}
	return &net.UDPAddr{IP: source.IP(), Port: 0}
}

func hasBindAddress(sockopt *internet.SocketConfig) bool {
	return sockopt != nil && len(sockopt.BindAddress) > 0 && sockopt.BindPort > 0
}

func (d *protectedSystemDialer) control(ctx context.Context, destination xraynet.Destination, sockopt *internet.SocketConfig, bindUDP bool) func(string, string, syscall.RawConn) error {
	return func(network, _ string, rawConn syscall.RawConn) error {
		if err := d.protect(network, destination.NetAddr(), rawConn); err != nil {
			return fmt.Errorf("failed to protect Xray outbound socket for %s: %w", destination.String(), err)
		}
		if sockopt == nil {
			return nil
		}

		var optionErr error
		var bindErr error
		if err := rawConn.Control(func(fd uintptr) {
			optionErr = d.applyOptions(network, destination.NetAddr(), fd, sockopt)
			if bindUDP {
				bindErr = bindPlatformUDPAddress(fd, sockopt.BindAddress, sockopt.BindPort)
			}
		}); err != nil {
			return err
		}
		if optionErr != nil {
			xrayerrors.LogInfoInner(ctx, optionErr, "failed to apply socket options")
		}
		if bindErr != nil {
			xrayerrors.LogInfoInner(ctx, bindErr, "failed to bind source address")
		}
		return nil
	}
}
