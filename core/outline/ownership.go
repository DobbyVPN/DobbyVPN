package outline

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime/debug"
	"sync"

	"core/log"
)

type ownedListener struct {
	net.Listener
	owner *OutlineDevice
}

func (l ownedListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return l.owner.track(conn)
}

type ownedConn struct {
	net.Conn
	owner  *OutlineDevice
	mu     sync.Mutex
	closed bool
}

func (c *ownedConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	if err := c.Conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	c.closed = true
	c.owner.mu.Lock()
	delete(c.owner.connections, c)
	c.owner.mu.Unlock()
	return nil
}

func (c *ownedConn) CloseWrite() error {
	if writer, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return writer.CloseWrite()
	}
	return nil
}

func (d *OutlineDevice) track(conn net.Conn) (net.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	owned := &ownedConn{Conn: conn, owner: d}
	d.connections[owned] = struct{}{}
	// A dial that finishes after shutdown began still transfers ownership.
	// Close drains these late handles after every worker has finished.
	if d.closing {
		return nil, net.ErrClosed
	}
	return owned, nil
}

// Submit implements the upstream SOCKS worker pool. Each child is registered
// while its parent is still owned, so Close waits for relays as well as accept.
func (d *OutlineDevice) Submit(work func()) error {
	d.workers.Add(1)
	go func() {
		defer d.workers.Done()
		defer func() {
			if value := recover(); value != nil {
				log.Errorf(Category, "Outline worker panic: %v\n%s", value, debug.Stack())
			}
		}()
		work()
	}()
	return nil
}

func (d *OutlineDevice) dialContext(parent context.Context) (operation context.Context, finish func()) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(d.ctx, cancel)
	return ctx, func() { stop(); cancel() }
}

// Resolve keeps SOCKS hostname lookups under the same owner as dialing. The
// upstream default uses net.ResolveIPAddr with a background context, which can
// leave Close waiting for DNS workers after the tunnel has already stopped.
func (d *OutlineDevice) Resolve(parent context.Context, name string) (context.Context, net.IP, error) {
	ctx, cancel := d.dialContext(parent)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, name)
	if err != nil {
		return parent, nil, err
	}
	// Preserve ResolveIPAddr's preference for IPv4 when resolving a hostname.
	for _, address := range addresses {
		if address.IP.To4() != nil {
			return parent, address.IP, nil
		}
	}
	if len(addresses) == 0 {
		return parent, nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
	}
	return parent, addresses[0].IP, nil
}

func (d *OutlineDevice) Close() error {
	if d == nil {
		return errors.New("outline device is not initialized")
	}
	d.closeMu.Lock()
	defer d.closeMu.Unlock()
	d.cancel()
	d.mu.Lock()
	d.closing = true
	connections := make([]*ownedConn, 0, len(d.connections))
	for conn := range d.connections {
		connections = append(connections, conn)
	}
	d.mu.Unlock()
	var result error
	if err := d.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		result = fmt.Errorf("close Outline SOCKS listener: %w", err)
	}
	for _, conn := range connections {
		result = errors.Join(result, conn.Close())
	}
	if result != nil {
		return result
	}
	d.workers.Wait()
	d.mu.Lock()
	connections = connections[:0]
	for conn := range d.connections {
		connections = append(connections, conn)
	}
	d.mu.Unlock()
	for _, conn := range connections {
		result = errors.Join(result, conn.Close())
	}
	return result
}
