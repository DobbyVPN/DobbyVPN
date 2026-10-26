package internal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"time"

	"core/dnscache"

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
	owner        context.Context
	resolver     *dnscache.Cache
	protect      socketProtector
	applyOptions outboundSocketOptions
	sockets      *transportSockets
}

func newProtectedSystemDialer(
	protect socketProtector,
	applyOptions outboundSocketOptions,
) *protectedSystemDialer {
	return &protectedSystemDialer{protect: protect, applyOptions: applyOptions, sockets: newTransportSockets()}
}

//nolint:revive // Required by Xray's internet.SystemDialer interface.
func (d *protectedSystemDialer) DestIpAddress() net.IP { return nil }

func (d *protectedSystemDialer) Dial(ctx context.Context, source xraynet.Address, destination xraynet.Destination, sockopt *internet.SocketConfig) (net.Conn, error) {
	if err := d.sockets.begin(); err != nil {
		return nil, err
	}
	defer d.sockets.end()
	ctx, cancelDial := context.WithCancel(ctx)
	stopDial := context.AfterFunc(d.sockets.ctx, cancelDial)
	defer stopDial()
	defer cancelDial()
	if err := d.sockets.ctx.Err(); err != nil {
		return nil, err
	}
	if d.owner != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		stop := context.AfterFunc(d.owner, cancel)
		defer stop()
		defer cancel()
		if err := d.owner.Err(); err != nil {
			return nil, err
		}
	}
	if destination.Address.Family().IsDomain() {
		addresses, err := d.resolver.ResolveAddresses(ctx, destination.Address.Domain())
		if err != nil {
			return nil, err
		}
		var failures []error
		for _, address := range addresses {
			resolved := destination
			resolved.Address = xraynet.IPAddress(address.IP)
			conn, err := d.dialResolved(ctx, source, resolved, net.JoinHostPort(address.String(), destination.Port.String()), sockopt)
			if err == nil {
				return conn, nil
			}
			xrayerrors.LogDebug(ctx, "underlying destination ", resolved.String(), " unavailable: ", err)
			failures = append(failures, err)
			if ctx.Err() != nil {
				break
			}
		}
		return nil, errors.Join(failures...)
	}
	return d.dialResolved(ctx, source, destination, destination.NetAddr(), sockopt)
}

// AfterFunc propagates cancellation asynchronously. Recheck the synchronous
// owner state before creating or protecting a socket after a native DNS query.
func (d *protectedSystemDialer) dialContextError(ctx context.Context) error {
	var ownerErr error
	if d.owner != nil {
		ownerErr = d.owner.Err()
	}
	return errors.Join(ctx.Err(), d.sockets.ctx.Err(), ownerErr)
}

func (d *protectedSystemDialer) trackSocket(ctx context.Context, raw io.Closer) (*transportSocket, error) {
	socket, err := d.sockets.track(raw)
	if err != nil {
		return nil, err
	}
	// Native socket creation can complete before AfterFunc propagates owner
	// cancellation. Keep failed closes owned, but never hand that socket out.
	if err := d.dialContextError(ctx); err != nil {
		return nil, errors.Join(err, socket.Close())
	}
	return socket, nil
}

func (d *protectedSystemDialer) dialResolved(ctx context.Context, source xraynet.Address, destination xraynet.Destination, address string, sockopt *internet.SocketConfig) (net.Conn, error) {
	if err := d.dialContextError(ctx); err != nil {
		return nil, err
	}
	switch destination.Network {
	case xraynet.Network_TCP:
		return d.dialStream(ctx, source, destination, address, sockopt)
	case xraynet.Network_UDP:
		return d.dialPacket(ctx, source, destination, address, sockopt)
	case xraynet.Network_Unknown, xraynet.Network_UNIX:
		return nil, fmt.Errorf("protected Xray dialer does not support network %q", destination.Network)
	default:
		return nil, fmt.Errorf("protected Xray dialer does not support network %q", destination.Network)
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
		Control:         d.control(ctx, destination, sockopt),
	}
	if sockopt != nil && sockopt.TcpMptcp {
		dialer.SetMultipathTCP(true)
	}
	conn, err := dialer.DialContext(ctx, destination.Network.SystemString(), address)
	if err != nil {
		return nil, err
	}
	socket, err := d.trackSocket(ctx, conn)
	if err != nil {
		return nil, err
	}
	return &ownedTCPConn{TCPConn: conn.(*net.TCPConn), socket: socket}, nil
}

func (d *protectedSystemDialer) dialPacket(ctx context.Context, source xraynet.Address, destination xraynet.Destination, address string, sockopt *internet.SocketConfig) (net.Conn, error) {
	network := "udp4"
	if destination.Address.Family().IsIPv6() {
		network = "udp6"
	}
	local := sourceAddr(xraynet.Network_UDP, source)
	if local == nil {
		local = &net.UDPAddr{IP: net.IPv4zero, Port: 0}
		if destination.Address.Family().IsIPv6() {
			local = &net.UDPAddr{IP: net.IPv6unspecified}
		}
	}
	destinationAddr, err := net.ResolveUDPAddr(network, address)
	if err != nil {
		return nil, err
	}
	// A wildcard bind succeeds even when this address family has no physical
	// route. Check the protected UDP route without sending a packet so a domain
	// can fall through to its next native DNS answer (including DNS64). Keep the
	// actual transport unconnected: QUIC needs RawConn and destination writes.
	routeDialer := net.Dialer{LocalAddr: local, Control: d.control(ctx, destination, sockopt)}
	route, err := routeDialer.DialContext(ctx, network, address)
	if err != nil {
		return nil, fmt.Errorf("physical UDP route to %s: %w", address, err)
	}
	if closeErr := route.Close(); closeErr != nil {
		return nil, fmt.Errorf("close UDP route check for %s: %w", address, closeErr)
	}
	listenConfig := net.ListenConfig{Control: d.control(ctx, destination, sockopt)}
	// An unspecified UDP bind can otherwise create a dual-stack IPv6 socket
	// for an IPv4 gateway, selecting the wrong physical-interface protection.
	packetConn, err := listenConfig.ListenPacket(ctx, network, local.String())
	if err != nil {
		return nil, err
	}
	socket, err := d.trackSocket(ctx, packetConn)
	if err != nil {
		return nil, err
	}
	return &internet.PacketConnWrapper{PacketConn: &ownedUDPConn{UDPConn: packetConn.(*net.UDPConn), socket: socket}, Dest: destinationAddr}, nil
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

func (d *protectedSystemDialer) control(ctx context.Context, destination xraynet.Destination, sockopt *internet.SocketConfig) func(string, string, syscall.RawConn) error {
	return func(network, _ string, rawConn syscall.RawConn) error {
		if err := d.dialContextError(ctx); err != nil {
			return err
		}
		var optionErr error
		if err := rawConn.Control(func(fd uintptr) {
			if sockopt != nil {
				optionErr = d.applyOptions(network, destination.NetAddr(), fd, sockopt)
			}
		}); err != nil {
			return err
		}
		if optionErr != nil {
			xrayerrors.LogInfoInner(ctx, optionErr, "failed to apply socket options")
		}
		// Apply app-owned routing protection last; accepted custom options must
		// never remove the mark/interface binding that keeps transport off TUN.
		if err := d.protect(network, destination.NetAddr(), rawConn); err != nil {
			return fmt.Errorf("failed to protect Xray outbound socket for %s: %w", destination.String(), err)
		}
		return nil
	}
}
