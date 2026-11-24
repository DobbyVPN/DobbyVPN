package tunnel

import (
	"context"
	"net"
	"net/netip"
	"os"
	"slices"
	"sync"
	"time"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	"github.com/xjasonlyu/tun2socks/v2/proxy"
	"golang.org/x/net/dns/dnsmessage"
)

type udpDNSOverTCPKey struct{}

// WithUDPDNSOverTCP marks an attempt whose protocol does not carry UDP DNS
// reliably. The tunnel then answers a DNS query sent over UDP to one of the
// tunnel resolvers at once with a truncated response, so the system resolver
// retries over TCP through the tunnel, as the Outline device does on Unix
// (#532). Other UDP to port 53 is not touched.
func WithUDPDNSOverTCP(ctx context.Context, resolvers []netip.Addr) context.Context {
	normalized := make([]netip.Addr, 0, len(resolvers))
	for _, resolver := range resolvers {
		normalized = append(normalized, resolver.Unmap().WithZone(""))
	}
	return context.WithValue(ctx, udpDNSOverTCPKey{}, normalized)
}

// UDPDNSOverTCPRequested reports whether ctx carries WithUDPDNSOverTCP.
func UDPDNSOverTCPRequested(ctx context.Context) bool {
	_, requested := ctx.Value(udpDNSOverTCPKey{}).([]netip.Addr)
	return requested
}

func udpDNSOverTCPResolvers(ctx context.Context) []netip.Addr {
	resolvers, _ := ctx.Value(udpDNSOverTCPKey{}).([]netip.Addr)
	return resolvers
}

// truncatingDNSProxy takes UDP to port 53 of the tunnel resolvers; all other
// UDP goes to the attempt's VPN proxy unchanged.
type truncatingDNSProxy struct {
	proxy.Proxy
	resolvers []netip.Addr
}

func (p truncatingDNSProxy) DialUDP(metadata *M.Metadata) (net.PacketConn, error) {
	if metadata.DstPort != 53 || !slices.Contains(p.resolvers, metadata.DstIP.Unmap().WithZone("")) {
		return p.Proxy.DialUDP(metadata)
	}
	return newDNSFallbackPacketConn(func() (net.PacketConn, error) { return p.Proxy.DialUDP(metadata) }), nil
}

// truncatedDNSResponse returns the response to a valid DNS query (QR clear,
// opcode QUERY, at least one parsable question): the header with QR and TC set
// and the questions, without answers or additional records. ok is false for
// anything else, which then goes to the resolver unchanged.
func truncatedDNSResponse(query []byte) (response []byte, ok bool) {
	var parser dnsmessage.Parser
	header, err := parser.Start(query)
	if err != nil || header.Response || header.OpCode != 0 {
		return nil, false
	}
	questions, qerr := parser.AllQuestions()
	if qerr != nil || len(questions) == 0 {
		return nil, false
	}
	builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{
		ID: header.ID, Response: true, Truncated: true,
		RecursionDesired: header.RecursionDesired, CheckingDisabled: header.CheckingDisabled,
	})
	if builder.StartQuestions() != nil {
		return nil, false
	}
	for _, question := range questions {
		if builder.Question(question) != nil {
			return nil, false
		}
	}
	response, err = builder.Finish()
	return response, err == nil
}

type dnsFallbackPacket struct {
	payload []byte
	from    net.Addr
}

// dnsFallbackPacketConn answers valid DNS queries with a truncated response
// itself and passes any other packet to the real VPN UDP path, which it opens
// only when such a packet arrives; replies of both reach the reader.
type dnsFallbackPacketConn struct {
	dial    func() (net.PacketConn, error)
	packets chan dnsFallbackPacket
	done    chan struct{}
	once    sync.Once

	mu       sync.Mutex
	real     net.PacketConn
	dialing  chan struct{} // closed when an in-flight dial ends
	deadline time.Time
	// deadlineChanged is closed and replaced on every read deadline change,
	// so a pending read recomputes its timer.
	deadlineChanged chan struct{}
}

func newDNSFallbackPacketConn(dial func() (net.PacketConn, error)) *dnsFallbackPacketConn {
	return &dnsFallbackPacketConn{
		dial: dial, packets: make(chan dnsFallbackPacket, 16), done: make(chan struct{}),
		deadlineChanged: make(chan struct{}),
	}
}

func (c *dnsFallbackPacketConn) deliver(packet dnsFallbackPacket) {
	select {
	case c.packets <- packet:
	case <-c.done:
	default: // the client is not reading; it will retry
	}
}

// realConn opens the VPN UDP path once. The dial runs without the lock, so
// Close and reads are not held up by it; a connection that arrives after
// Close is closed instead of kept.
func (c *dnsFallbackPacketConn) realConn() (net.PacketConn, error) {
	for {
		c.mu.Lock()
		if c.real != nil {
			conn := c.real
			c.mu.Unlock()
			return conn, nil
		}
		select {
		case <-c.done:
			c.mu.Unlock()
			return nil, net.ErrClosed
		default:
		}
		if wait := c.dialing; wait != nil {
			c.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-c.done:
				return nil, net.ErrClosed
			}
		}
		dialing := make(chan struct{})
		c.dialing = dialing
		c.mu.Unlock()

		conn, err := c.dial()

		c.mu.Lock()
		c.dialing = nil
		close(dialing)
		closed := false
		select {
		case <-c.done:
			closed = true
		default:
		}
		if err == nil && !closed {
			c.real = conn
		}
		c.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if closed {
			_ = conn.Close()
			return nil, net.ErrClosed
		}
		go c.pump(conn)
		return conn, nil
	}
}

func (c *dnsFallbackPacketConn) pump(conn net.PacketConn) {
	buf := make([]byte, 64*1024)
	for {
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			return
		}
		c.deliver(dnsFallbackPacket{payload: append([]byte(nil), buf[:n]...), from: from})
	}
}

func (c *dnsFallbackPacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	select {
	case <-c.done:
		return 0, net.ErrClosed
	default:
	}
	if response, ok := truncatedDNSResponse(b); ok {
		c.deliver(dnsFallbackPacket{payload: response, from: addr})
		return len(b), nil
	}
	conn, err := c.realConn()
	if err != nil {
		return 0, err
	}
	return conn.WriteTo(b, addr)
}

func (c *dnsFallbackPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	for {
		c.mu.Lock()
		deadline, changed := c.deadline, c.deadlineChanged
		c.mu.Unlock()
		var expired <-chan time.Time
		var timer *time.Timer
		if !deadline.IsZero() {
			timer = time.NewTimer(time.Until(deadline))
			expired = timer.C
		}
		select {
		case packet := <-c.packets:
			stopTimer(timer)
			return copy(b, packet.payload), packet.from, nil
		case <-c.done:
			stopTimer(timer)
			return 0, nil, net.ErrClosed
		case <-expired:
			return 0, nil, os.ErrDeadlineExceeded
		case <-changed:
			stopTimer(timer) // the deadline moved; wait with the new one
		}
	}
}

func stopTimer(timer *time.Timer) {
	if timer != nil {
		timer.Stop()
	}
}

func (c *dnsFallbackPacketConn) Close() error {
	c.once.Do(func() { close(c.done) })
	c.mu.Lock()
	conn := c.real
	c.mu.Unlock()
	if conn != nil {
		return conn.Close()
	}
	return nil
}

func (c *dnsFallbackPacketConn) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4zero, Port: 0}
}

func (c *dnsFallbackPacketConn) SetDeadline(t time.Time) error { return c.SetReadDeadline(t) }

func (c *dnsFallbackPacketConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	close(c.deadlineChanged)
	c.deadlineChanged = make(chan struct{})
	c.mu.Unlock()
	return nil
}

func (c *dnsFallbackPacketConn) SetWriteDeadline(time.Time) error { return nil }
