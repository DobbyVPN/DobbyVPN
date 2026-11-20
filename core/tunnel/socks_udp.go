package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"time"

	"core/log"

	"github.com/xjasonlyu/tun2socks/v2/buffer"
	"github.com/xjasonlyu/tun2socks/v2/dialer"
	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	"github.com/xjasonlyu/tun2socks/v2/transport/socks5"
)

// A connected UDP socket lets the kernel enforce the SOCKS association's
// negotiated peer for both reads and writes. The TCP connection owns its lifetime.
func (p *socksTCPProxy) DialUDP(_ *M.Metadata) (net.PacketConn, error) {
	// Preserve the underlying proxy's five-second control connection allowance.
	parent, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx, finish := p.owner.pendingTCPContext(parent)
	defer finish()
	target := socks5.SerializeAddr("", netip.IPv4Unspecified(), 0)
	control, bound, err := p.negotiate(ctx, socks5.CmdUDPAssociate, target)
	if err != nil {
		return nil, err
	}
	relay := bound.UDPAddr()
	if relay == nil || relay.Port == 0 {
		return nil, errors.Join(errors.New("invalid SOCKS UDP relay binding"), control.Close())
	}
	if relay.IP.IsUnspecified() {
		peer, ok := control.RemoteAddr().(*net.TCPAddr)
		if !ok {
			return nil, errors.Join(errors.New("SOCKS control peer has no IP address"), control.Close())
		}
		relay.IP, relay.Zone = peer.IP, peer.Zone
	}
	socket, err := dialer.DialContext(ctx, "udp", relay.String())
	if err != nil {
		return nil, errors.Join(fmt.Errorf("connect to negotiated UDP relay: %w", err), control.Close())
	}
	association := &relayPacketConn{Conn: socket, control: control}
	go func() {
		_, err := io.Copy(io.Discard, control)
		if err != nil && !errors.Is(err, net.ErrClosed) {
			log.Debugf(Category, "SOCKS UDP control connection ended: %v", err)
		}
		if err := association.Close(); err != nil {
			log.Errorf(Category, "SOCKS UDP association cleanup failed: %v", err)
		}
	}()
	return association, nil
}

type relayPacketConn struct {
	net.Conn
	control net.Conn
}

func (c *relayPacketConn) WriteTo(payload []byte, destination net.Addr) (int, error) {
	packet, err := socks5.EncodeUDPPacket(socks5.ParseAddr(destination), payload)
	if err != nil {
		return 0, err
	}
	n, err := c.Write(packet)
	if n == len(packet) {
		return len(payload), err
	}
	if err == nil {
		err = io.ErrShortWrite
	}
	return 0, err
}

func (c *relayPacketConn) ReadFrom(dst []byte) (int, net.Addr, error) {
	packet := dst
	if len(dst) < buffer.MaxSegmentSize {
		packet = buffer.Get(min(buffer.MaxSegmentSize, len(dst)+3+socks5.MaxAddrLen))
		defer func() { _ = buffer.Put(packet) }()
	}
	n, err := c.Read(packet)
	if err != nil {
		return 0, nil, err
	}
	address, payload, err := socks5.DecodeUDPPacket(packet[:n])
	if err != nil {
		return 0, nil, err
	}
	source := address.UDPAddr()
	if source == nil {
		return 0, nil, errors.New("invalid SOCKS datagram address")
	}
	return copy(dst, payload), source, nil
}

func (c *relayPacketConn) Close() error {
	var closeErr error
	for _, socket := range []net.Conn{c.control, c.Conn} {
		if err := socket.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			closeErr = errors.Join(closeErr, err)
		}
	}
	return closeErr
}
