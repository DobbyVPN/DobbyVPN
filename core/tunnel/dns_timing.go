package tunnel

import (
	"net"
	"sync/atomic"
	"time"

	"core/log"

	"github.com/xjasonlyu/tun2socks/v2/core/adapter"
)

// dnsTiming records, once per attached transport and protocol, how long after
// the transport was attached the packet stack saw the first DNS query (port
// 53) and the first answer to it. The first lookup after connect is where a
// slow resolver path shows (#532); later lookups are cached or fast.
type dnsTiming struct {
	attached   time.Time
	generation uint64
	// One flag per protocol (0 UDP, 1 TCP) and event.
	queried, answered [2]atomic.Bool
}

func newDNSTiming(generation uint64) *dnsTiming {
	return &dnsTiming{attached: time.Now(), generation: generation}
}

func dnsProtocol(index int) string {
	if index == 1 {
		return "tcp"
	}
	return "udp"
}

// query logs the first DNS flow of its protocol since the transport was
// attached, and reports whether flows of the protocol still need watching for
// the first answer: until one is answered, every flow is watched, so a first
// flow that never gets an answer does not hide the answer of a later one.
func (t *dnsTiming) query(index int, server net.Addr, at time.Time) bool {
	if t == nil || t.answered[index].Load() {
		return false
	}
	if t.queried[index].CompareAndSwap(false, true) {
		log.Info(Category, "first DNS query seen by the tunnel", map[string]any{
			"generation": t.generation, "protocol": dnsProtocol(index), "server": addrString(server),
			"since_transport_attached_ms": at.Sub(t.attached).Milliseconds(),
		})
	}
	return true
}

func (t *dnsTiming) answer(index int, server net.Addr, queried time.Time) {
	if t == nil || !t.answered[index].CompareAndSwap(false, true) {
		return
	}
	now := time.Now()
	log.Info(Category, "first DNS answer seen by the tunnel", map[string]any{
		"generation": t.generation, "protocol": dnsProtocol(index), "server": addrString(server),
		"since_transport_attached_ms": now.Sub(t.attached).Milliseconds(),
		"since_query_ms":              now.Sub(queried).Milliseconds(),
	})
}

func addrString(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	return addr.String()
}

// dnsTimedUDP reports the first answer the stack writes back to a DNS client.
type dnsTimedUDP struct {
	adapter.UDPConn
	timing  *dnsTiming
	queried time.Time
}

func (c *dnsTimedUDP) Write(b []byte) (int, error) {
	n, err := c.UDPConn.Write(b)
	if err == nil {
		c.timing.answer(0, c.LocalAddr(), c.queried)
	}
	return n, err
}

func (c *dnsTimedUDP) WriteTo(b []byte, addr net.Addr) (int, error) {
	n, err := c.UDPConn.WriteTo(b, addr)
	if err == nil {
		c.timing.answer(0, c.LocalAddr(), c.queried)
	}
	return n, err
}

// dnsTimedTCP reports the first answer bytes on a DNS-over-TCP flow.
type dnsTimedTCP struct {
	adapter.TCPConn
	timing  *dnsTiming
	queried time.Time
}

// CloseRead and CloseWrite keep the origin's half-close, which the relay uses
// to pass EOF on; adapter.TCPConn does not declare them.
func (c *dnsTimedTCP) CloseRead() error {
	if conn, ok := c.TCPConn.(interface{ CloseRead() error }); ok {
		return conn.CloseRead()
	}
	return c.Close()
}

func (c *dnsTimedTCP) CloseWrite() error {
	if conn, ok := c.TCPConn.(interface{ CloseWrite() error }); ok {
		return conn.CloseWrite()
	}
	return c.Close()
}

func (c *dnsTimedTCP) Write(b []byte) (int, error) {
	n, err := c.TCPConn.Write(b)
	if err == nil && n > 0 {
		c.timing.answer(1, c.LocalAddr(), c.queried)
	}
	return n, err
}

// timeDNSUDP wraps the first UDP flow to port 53 of the attempt. The packet
// stack's local side of a flow is the destination the client addressed.
func timeDNSUDP(timing *dnsTiming, conn adapter.UDPConn) adapter.UDPConn {
	if timing == nil || conn.ID().LocalPort != 53 {
		return conn
	}
	now := time.Now()
	if !timing.query(0, conn.LocalAddr(), now) {
		return conn
	}
	return &dnsTimedUDP{UDPConn: conn, timing: timing, queried: now}
}

func timeDNSTCP(timing *dnsTiming, conn adapter.TCPConn) adapter.TCPConn {
	if timing == nil || conn.ID().LocalPort != 53 {
		return conn
	}
	now := time.Now()
	if !timing.query(1, conn.LocalAddr(), now) {
		return conn
	}
	return &dnsTimedTCP{TCPConn: conn, timing: timing, queried: now}
}
