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
		ID: header.ID, Response: true, Truncated: true, RecursionDesired: header.RecursionDesired,
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
	deadline time.Time
}

func newDNSFallbackPacketConn(dial func() (net.PacketConn, error)) *dnsFallbackPacketConn {
	return &dnsFallbackPacketConn{dial: dial, packets: make(chan dnsFallbackPacket, 16), done: make(chan struct{})}
}

func (c *dnsFallbackPacketConn) deliver(packet dnsFallbackPacket) {
	select {
	case c.packets <- packet:
	case <-c.done:
	default: // the client is not reading; it will retry
	}
}

func (c *dnsFallbackPacketConn) realConn() (net.PacketConn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.real != nil {
		return c.real, nil
	}
	select {
	case <-c.done:
		return nil, net.ErrClosed
	default:
	}
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	c.real = conn
	go func() {
		buf := make([]byte, 64*1024)
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			c.deliver(dnsFallbackPacket{payload: append([]byte(nil), buf[:n]...), from: from})
		}
	}()
	return conn, nil
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
	case packet := <-c.packets:
		return copy(b, packet.payload), packet.from, nil
	case <-c.done:
		return 0, nil, net.ErrClosed
	case <-expired:
		return 0, nil, os.ErrDeadlineExceeded
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
	c.mu.Unlock()
	return nil
}

func (c *dnsFallbackPacketConn) SetWriteDeadline(time.Time) error { return nil }
