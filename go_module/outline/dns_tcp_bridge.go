package outline

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const (
	dnsHeaderLength         = 12
	maxDNSMessageLength     = 1<<16 - 1
	maxDNSUDPResponseLength = 32 * 1024 // Matches go-socks5's UDP relay buffer.
	dnsTCPExchangeTimeout   = 5 * time.Second
)

var (
	errInvalidDNSQuery    = errors.New("invalid DNS query")
	errInvalidDNSResponse = errors.New("invalid DNS response")
)

type dnsTCPDialFunc func(context.Context, string) (net.Conn, error)

type dnsBridgeAddr string

func (a dnsBridgeAddr) Network() string { return "udp" }
func (a dnsBridgeAddr) String() string  { return string(a) }

// dnsTCPBridgeConn adapts the SOCKS UDP relay's net.Conn interface to one
// DNS-over-TCP transaction per UDP query. Queries on a relay association stay
// serialized until the preceding response has been read completely.
type dnsTCPBridgeConn struct {
	mu      sync.Mutex
	ready   *sync.Cond
	ctx     context.Context
	cancel  context.CancelFunc
	dial    dnsTCPDialFunc
	addr    string
	pending []byte
	readErr error
	writing bool
	closed  bool
}

func newDNSTCPBridgeConn(parent context.Context, addr string, dial dnsTCPDialFunc) *dnsTCPBridgeConn {
	ctx, cancel := context.WithCancel(parent)
	c := &dnsTCPBridgeConn{ctx: ctx, cancel: cancel, dial: dial, addr: addr}
	c.ready = sync.NewCond(&c.mu)
	context.AfterFunc(ctx, func() { _ = c.Close() })
	return c
}

func (c *dnsTCPBridgeConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.pending) == 0 && c.readErr == nil && !c.closed {
		c.ready.Wait()
	}
	if c.readErr != nil {
		err := c.readErr
		c.readErr = nil
		return 0, err
	}
	if len(c.pending) == 0 && c.closed {
		return 0, net.ErrClosed
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	if len(c.pending) == 0 {
		c.pending = nil
		c.ready.Broadcast()
	}
	return n, nil
}

func (c *dnsTCPBridgeConn) Write(query []byte) (int, error) {
	if len(query) == 0 {
		return 0, nil
	}
	if !validDNSQuery(query) {
		return 0, errInvalidDNSQuery
	}
	query = append([]byte(nil), query...)

	c.mu.Lock()
	for (len(c.pending) != 0 || c.writing) && !c.closed && c.readErr == nil {
		c.ready.Wait()
	}
	if c.closed {
		c.mu.Unlock()
		return 0, net.ErrClosed
	}
	if c.readErr != nil {
		err := c.readErr
		c.mu.Unlock()
		return 0, err
	}
	c.writing = true
	c.mu.Unlock()

	response, err := c.exchange(query)
	c.mu.Lock()
	c.writing = false
	if c.closed {
		c.ready.Broadcast()
		c.mu.Unlock()
		return 0, net.ErrClosed
	}
	if err != nil {
		c.readErr = err
		c.ready.Broadcast()
		c.mu.Unlock()
		return 0, err
	}
	c.pending = response
	c.ready.Broadcast()
	c.mu.Unlock()
	return len(query), nil
}

func (c *dnsTCPBridgeConn) exchange(query []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(c.ctx, dnsTCPExchangeTimeout)
	defer cancel()
	stream, err := c.dial(ctx, c.addr)
	if err != nil {
		return nil, dnsBridgeIOError(ctx, err)
	}
	defer stream.Close()
	stopOnCancel := context.AfterFunc(ctx, func() { _ = stream.Close() })
	defer stopOnCancel()
	if err := ctx.Err(); err != nil {
		return nil, dnsBridgeIOError(ctx, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := stream.SetDeadline(deadline); err != nil {
			return nil, err
		}
	}

	frame := make([]byte, 2+len(query))
	binary.BigEndian.PutUint16(frame[:2], uint16(len(query)))
	copy(frame[2:], query)
	if err := writeDNSFull(stream, frame); err != nil {
		return nil, dnsBridgeIOError(ctx, err)
	}
	var lengthPrefix [2]byte
	if _, err := io.ReadFull(stream, lengthPrefix[:]); err != nil {
		return nil, dnsBridgeIOError(ctx, err)
	}
	responseLength := int(binary.BigEndian.Uint16(lengthPrefix[:]))
	if responseLength < dnsHeaderLength || responseLength > maxDNSUDPResponseLength {
		return nil, fmt.Errorf("%w: response length %d is outside UDP bounds", errInvalidDNSResponse, responseLength)
	}
	response := make([]byte, responseLength)
	if _, err := io.ReadFull(stream, response); err != nil {
		return nil, dnsBridgeIOError(ctx, err)
	}
	var message dnsmessage.Message
	if err := message.Unpack(response); err != nil || message.Header.ID != binary.BigEndian.Uint16(query[:2]) || !message.Header.Response {
		return nil, errInvalidDNSResponse
	}
	return response, nil
}

func writeDNSFull(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if n > 0 {
			b = b[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func dnsBridgeIOError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return os.ErrDeadlineExceeded
	}
	return err
}

func validDNSQuery(query []byte) bool {
	if len(query) > maxDNSMessageLength {
		return false
	}
	var message dnsmessage.Message
	return message.Unpack(query) == nil && !message.Header.Response && len(message.Questions) > 0
}

func (c *dnsTCPBridgeConn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.pending = nil
	c.ready.Broadcast()
	c.mu.Unlock()
	c.cancel()
	return nil
}

func (c *dnsTCPBridgeConn) LocalAddr() net.Addr  { return nil }
func (c *dnsTCPBridgeConn) RemoteAddr() net.Addr { return dnsBridgeAddr(c.addr) }

// The SOCKS UDP relay does not set conn deadlines. Each network exchange is
// bounded by dnsTCPExchangeTimeout and the caller's context.
func (c *dnsTCPBridgeConn) SetDeadline(time.Time) error      { return nil }
func (c *dnsTCPBridgeConn) SetReadDeadline(time.Time) error  { return nil }
func (c *dnsTCPBridgeConn) SetWriteDeadline(time.Time) error { return nil }
