package outline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"runtime/debug"
	"runtime/pprof"
	"sync"
	"sync/atomic"
	"time"

	"core/log"
)

type streamConnScopeContextKey struct{}

// pendingStreamConnScope owns the raw stream until the configured transport
// finishes its TLS and WebSocket handshakes. The operation context can cancel
// a TCP dial, but it cannot interrupt HTTP response reads after the dialer has
// returned the socket, so the owner closes that pending socket directly.
type pendingStreamConnScope struct {
	ctx       context.Context
	mu        sync.Mutex
	pending   map[net.Conn]struct{}
	aborted   bool
	handedOff bool
	stop      func() bool
	done      chan struct{}
	doneOnce  sync.Once
}

func newPendingStreamConnScope(ctx context.Context) *pendingStreamConnScope {
	scope := &pendingStreamConnScope{
		ctx:     ctx,
		pending: make(map[net.Conn]struct{}),
		done:    make(chan struct{}),
	}
	scope.stop = context.AfterFunc(ctx, func() {
		defer scope.markDone()
		scope.abort()
	})
	return scope
}

func (s *pendingStreamConnScope) markDone() {
	s.doneOnce.Do(func() { close(s.done) })
}

func (s *pendingStreamConnScope) register(conn net.Conn) error {
	s.mu.Lock()
	if err := s.ctx.Err(); s.aborted || s.handedOff || err != nil {
		if !s.handedOff {
			s.aborted = true
		}
		pending := s.takePendingLocked()
		s.mu.Unlock()
		closeStreamConns(pending)
		_ = conn.Close()
		if err != nil {
			return err
		}
		return net.ErrClosed
	}
	s.pending[conn] = struct{}{}
	s.mu.Unlock()
	return nil
}

func (s *pendingStreamConnScope) handoff() bool {
	s.mu.Lock()
	if s.aborted || s.ctx.Err() != nil {
		s.aborted = true
		pending := s.takePendingLocked()
		s.mu.Unlock()
		closeStreamConns(pending)
		return false
	}
	s.handedOff = true
	s.pending = nil
	s.mu.Unlock()
	s.stopAndWait()
	return true
}

func (s *pendingStreamConnScope) abort() {
	s.mu.Lock()
	if s.handedOff {
		s.mu.Unlock()
		return
	}
	s.aborted = true
	pending := s.takePendingLocked()
	s.mu.Unlock()
	closeStreamConns(pending)
}

func (s *pendingStreamConnScope) takePendingLocked() []net.Conn {
	pending := make([]net.Conn, 0, len(s.pending))
	for conn := range s.pending {
		pending = append(pending, conn)
	}
	s.pending = nil
	return pending
}

func closeStreamConns(conns []net.Conn) {
	for _, conn := range conns {
		_ = conn.Close()
	}
}

func (s *pendingStreamConnScope) stopAndWait() {
	if s.stop() {
		s.markDone()
		return
	}
	<-s.done
}

func (s *pendingStreamConnScope) discard() {
	s.abort()
	s.stopAndWait()
}

func registerPendingStreamConn(ctx context.Context, conn net.Conn) error {
	scope, _ := ctx.Value(streamConnScopeContextKey{}).(*pendingStreamConnScope)
	if scope == nil {
		return nil
	}
	return scope.register(conn)
}

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
	d.activeWorkers.Add(1)
	go func() {
		defer func() {
			d.activeWorkers.Add(-1)
			d.workers.Done()
		}()
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

// Resolve leaves the application destination hostname in the SOCKS request.
// Shadowsocks carries it inside the encrypted connection for remote resolution.
// A local lookup would depend on VPN DNS before readiness admits application
// packets. Only the transport gateway uses the underlying-network resolver.
func (d *OutlineDevice) Resolve(parent context.Context, _ string) (context.Context, net.IP, error) {
	if err := parent.Err(); err != nil {
		return parent, nil, err
	}
	return parent, nil, d.ctx.Err()
}

func (d *OutlineDevice) Close() error {
	if d == nil {
		return errors.New("outline device is not initialized")
	}
	started := time.Now()
	var phase atomic.Value
	phase.Store("close-lock")
	// This trigger only records diagnostics; cleanup still owns every resource.
	timer := time.AfterFunc(3*time.Second, func() {
		var stacks bytes.Buffer
		err := pprof.Lookup("goroutine").WriteTo(&stacks, 2)
		log.Warnf(Category, "Outline close slow phase=%s elapsed=%s activeWorkers=%d stackError=%v\n%s", phase.Load(), time.Since(started), d.activeWorkers.Load(), err, stacks.String())
	})
	defer timer.Stop()
	setPhase := func(name string) { phase.Store(name) }
	log.Infof(Category, "Outline close started activeWorkers=%d", d.activeWorkers.Load())
	d.closeMu.Lock()
	defer d.closeMu.Unlock()
	setPhase("cancel")
	phaseStarted := time.Now()
	log.Infof(Category, "Outline close phase=cancel started activeWorkers=%d", d.activeWorkers.Load())
	d.cancel()
	log.Infof(Category, "Outline close phase=cancel finished elapsed=%s activeWorkers=%d", time.Since(phaseStarted), d.activeWorkers.Load())
	d.mu.Lock()
	d.closing = true
	connections := make([]*ownedConn, 0, len(d.connections))
	for conn := range d.connections {
		connections = append(connections, conn)
	}
	d.mu.Unlock()
	var result error
	setPhase("listener-close")
	phaseStarted = time.Now()
	log.Infof(Category, "Outline close phase=listener-close started connections=%d activeWorkers=%d", len(connections), d.activeWorkers.Load())
	if err := d.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		result = fmt.Errorf("close Outline SOCKS listener: %w", err)
	}
	log.Infof(Category, "Outline close phase=listener-close finished elapsed=%s activeWorkers=%d error=%v", time.Since(phaseStarted), d.activeWorkers.Load(), result)
	setPhase("connection-close")
	for i, conn := range connections {
		phaseStarted = time.Now()
		log.Infof(Category, "Outline close phase=connection-close started index=%d count=%d type=%T activeWorkers=%d", i+1, len(connections), conn.Conn, d.activeWorkers.Load())
		err := conn.Close()
		result = errors.Join(result, err)
		log.Infof(Category, "Outline close phase=connection-close finished index=%d count=%d type=%T elapsed=%s activeWorkers=%d error=%v", i+1, len(connections), conn.Conn, time.Since(phaseStarted), d.activeWorkers.Load(), err)
	}
	if result != nil {
		log.Infof(Category, "Outline close finished elapsed=%s activeWorkers=%d error=%v", time.Since(started), d.activeWorkers.Load(), result)
		return result
	}
	setPhase("worker-drain")
	phaseStarted = time.Now()
	log.Infof(Category, "Outline close phase=worker-drain started activeWorkers=%d", d.activeWorkers.Load())
	d.workers.Wait()
	log.Infof(Category, "Outline close phase=worker-drain finished elapsed=%s activeWorkers=%d", time.Since(phaseStarted), d.activeWorkers.Load())
	setPhase("late-connection-snapshot")
	d.mu.Lock()
	connections = connections[:0]
	for conn := range d.connections {
		connections = append(connections, conn)
	}
	d.mu.Unlock()
	setPhase("late-connection-close")
	log.Infof(Category, "Outline close phase=late-connection-close started count=%d activeWorkers=%d", len(connections), d.activeWorkers.Load())
	for i, conn := range connections {
		phaseStarted = time.Now()
		log.Infof(Category, "Outline close phase=late-connection-close item started index=%d count=%d type=%T activeWorkers=%d", i+1, len(connections), conn.Conn, d.activeWorkers.Load())
		err := conn.Close()
		result = errors.Join(result, err)
		log.Infof(Category, "Outline close phase=late-connection-close item finished index=%d count=%d type=%T elapsed=%s activeWorkers=%d error=%v", i+1, len(connections), conn.Conn, time.Since(phaseStarted), d.activeWorkers.Load(), err)
	}
	setPhase("complete")
	log.Infof(Category, "Outline close finished elapsed=%s activeWorkers=%d error=%v", time.Since(started), d.activeWorkers.Load(), result)
	return result
}
