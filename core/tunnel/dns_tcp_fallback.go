package tunnel

import (
	"context"
	"net"
	"os"
	"sync"
	"time"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	"github.com/xjasonlyu/tun2socks/v2/proxy"
)

type udpDNSOverTCPKey struct{}

// WithUDPDNSOverTCP marks an attempt whose protocol does not carry UDP DNS
// reliably. The tunnel then answers every UDP query to port 53 at once with
// the query marked as a truncated response, so the system resolver retries
// over TCP through the tunnel, as the Outline device does on Unix (#532).
func WithUDPDNSOverTCP(ctx context.Context) context.Context {
	return context.WithValue(ctx, udpDNSOverTCPKey{}, true)
}

// UDPDNSOverTCPRequested reports whether ctx carries WithUDPDNSOverTCP.
func UDPDNSOverTCPRequested(ctx context.Context) bool {
	forced, _ := ctx.Value(udpDNSOverTCPKey{}).(bool)
	return forced
}

// truncatingDNSProxy dials UDP to port 53 locally; everything else goes to
// the attempt's VPN proxy.
type truncatingDNSProxy struct{ proxy.Proxy }

func (p truncatingDNSProxy) DialUDP(metadata *M.Metadata) (net.PacketConn, error) {
	if metadata.DstPort != 53 {
		return p.Proxy.DialUDP(metadata)
	}
	return newTruncatedDNSPacketConn(), nil
}

type truncatedDNSReply struct {
	payload []byte
	from    net.Addr
}

// truncatedDNSPacketConn returns each DNS query it is sent as a truncated
// response with no answers (QR and TC set, ANCOUNT 0). A packet too short to
// be DNS gets no reply.
type truncatedDNSPacketConn struct {
	replies chan truncatedDNSReply
	done    chan struct{}
	once    sync.Once

	mu       sync.Mutex
	deadline time.Time
}

func newTruncatedDNSPacketConn() *truncatedDNSPacketConn {
	return &truncatedDNSPacketConn{replies: make(chan truncatedDNSReply, 16), done: make(chan struct{})}
}

func (c *truncatedDNSPacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	select {
	case <-c.done:
		return 0, net.ErrClosed
	default:
	}
	if len(b) < 12 {
		return len(b), nil
	}
	reply := append([]byte(nil), b...)
	reply[2] |= 0x80 // response
	reply[2] |= 0x02 // truncated: retry over TCP
	reply[6], reply[7] = 0, 0
	select {
	case c.replies <- truncatedDNSReply{payload: reply, from: addr}:
	default: // the client is not reading; it will retry
	}
	return len(b), nil
}

func (c *truncatedDNSPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	c.mu.Lock()
	deadline := c.deadline
	c.mu.Unlock()
	var expired <-chan time.Time
	if !deadline.IsZero() {
		timer := time.NewTimer(time.Until(deadline))
		defer timer.Stop()
		expired = timer.C
	}
	select {
	case reply := <-c.replies:
		return copy(b, reply.payload), reply.from, nil
	case <-c.done:
		return 0, nil, net.ErrClosed
	case <-expired:
		return 0, nil, os.ErrDeadlineExceeded
	}
}

func (c *truncatedDNSPacketConn) Close() error {
	c.once.Do(func() { close(c.done) })
	return nil
}

func (c *truncatedDNSPacketConn) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4zero, Port: 0}
}

func (c *truncatedDNSPacketConn) SetDeadline(t time.Time) error { return c.SetReadDeadline(t) }

func (c *truncatedDNSPacketConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	return nil
}

func (c *truncatedDNSPacketConn) SetWriteDeadline(time.Time) error { return nil }
