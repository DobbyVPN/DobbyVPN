package trusttunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"core/dnscache"
	"core/log"
	"core/tunnel/protected_dialer"
)

// The native location pinger does not call the bridge's socket protector.
// A local endpoint keeps both its bootstrap and subsequent transport sockets
// inside this attempt's protected dialer. TLS still uses the original hostname.
type endpointForwarder struct {
	ctx         context.Context
	cancel      context.CancelFunc
	address     string
	tcp         net.Listener
	udp         net.PacketConn
	cache       *dnscache.Cache
	mu          sync.Mutex
	closed      bool
	connections map[*endpointConn]struct{}
	peers       map[string]*endpointConn
	workers     sync.WaitGroup
}

type endpointConn struct {
	net.Conn
	mu     sync.Mutex
	closed bool
}

func (c *endpointConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	if err := c.Conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	c.closed = true
	return nil
}

func (c *endpointConn) CloseWrite() error {
	if half, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return half.CloseWrite()
	}
	return nil
}

type endpointPairListener interface {
	Listen(context.Context, string, string) (net.Listener, error)
	ListenPacket(context.Context, string, string) (net.PacketConn, error)
}

// TCP and UDP have separate port namespaces; reserve TCP until UDP binds.
func listenEndpointPair(ctx context.Context, listener endpointPairListener) (net.Listener, net.PacketConn, error) {
	const maxAttempts = 16
	var last error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, errors.Join(last, err)
		}
		tcp, err := listener.Listen(ctx, "tcp4", "127.0.0.1:0")
		if err != nil {
			log.Warnf("trusttunnel", "endpoint pair attempt=%d network=tcp4 listen failed: %v", attempt, err)
			return nil, nil, errors.Join(last, err)
		}
		candidate := tcp.Addr().String()
		if ctxErr := ctx.Err(); ctxErr != nil {
			closeErr := tcp.Close()
			log.Warnf("trusttunnel", "endpoint pair attempt=%d candidate=%s network=tcp4 canceled=%v close=%v", attempt, candidate, ctxErr, closeErr)
			return nil, nil, errors.Join(last, ctxErr, closeErr)
		}
		udp, err := listener.ListenPacket(ctx, "udp4", candidate)
		if err == nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				tcpErr, udpErr := tcp.Close(), udp.Close()
				log.Warnf("trusttunnel", "endpoint pair attempt=%d candidate=%s canceled=%v tcp_close=%v udp_close=%v", attempt, candidate, ctxErr, tcpErr, udpErr)
				return nil, nil, errors.Join(last, ctxErr, tcpErr, udpErr)
			}
			if attempt > 1 {
				log.Infof("trusttunnel", "endpoint pair recovered attempt=%d candidate=%s", attempt, candidate)
			}
			return tcp, udp, nil
		}
		closeErr := tcp.Close()
		log.Warnf("trusttunnel", "endpoint pair attempt=%d candidate=%s network=udp4 bind failed=%v tcp_close=%v", attempt, candidate, err, closeErr)
		last = errors.Join(last, fmt.Errorf("endpoint pair attempt %d candidate %s udp4: %w", attempt, candidate, err))
		if closeErr != nil || ctx.Err() != nil || !retryEndpointPairBind(err) {
			return nil, nil, errors.Join(last, ctx.Err(), closeErr)
		}
	}
	return nil, nil, fmt.Errorf("endpoint pair exhausted %d candidates: %w", maxAttempts, last)
}

func newEndpointForwarder(ctx context.Context, cache *dnscache.Cache, address string) (*endpointForwarder, error) {
	ctx, cancel := context.WithCancel(ctx)
	tcp, udp, err := listenEndpointPair(ctx, &net.ListenConfig{})
	if err != nil {
		cancel()
		return nil, err
	}
	f := &endpointForwarder{ctx: ctx, cancel: cancel, address: address, tcp: tcp, udp: udp, cache: cache,
		connections: make(map[*endpointConn]struct{}), peers: make(map[string]*endpointConn)}
	f.workers.Add(2)
	go f.acceptTCP()
	go f.receiveUDP()
	return f, nil
}

func (f *endpointForwarder) localAddress() string { return f.tcp.Addr().String() }

func (f *endpointForwarder) track(conn net.Conn) *endpointConn {
	owned := &endpointConn{Conn: conn}
	f.mu.Lock()
	f.connections[owned] = struct{}{}
	closed := f.closed
	f.mu.Unlock()
	if closed {
		_ = f.release(owned)
		return nil
	}
	return owned
}

func (f *endpointForwarder) release(conn *endpointConn) error {
	if err := conn.Close(); err != nil {
		log.Warnf("trusttunnel", "endpoint socket close failed destination=%s: %v", f.address, err)
		return err
	}
	f.mu.Lock()
	delete(f.connections, conn)
	f.mu.Unlock()
	return nil
}

func (f *endpointForwarder) acceptTCP() {
	defer f.workers.Done()
	for {
		local, err := f.tcp.Accept()
		if err != nil {
			return
		}
		ownedLocal := f.track(local)
		if ownedLocal == nil {
			return
		}
		f.workers.Add(1)
		go func() {
			defer f.workers.Done()
			defer func() { _ = f.release(ownedLocal) }()
			remote, err := protected_dialer.DialContextWithProtect(f.ctx, f.cache, "tcp", f.address)
			if err != nil {
				if f.ctx.Err() == nil {
					log.Warnf("trusttunnel", "endpoint TCP dial failed destination=%s: %v", f.address, err)
				}
				return
			}
			ownedRemote := f.track(remote)
			if ownedRemote == nil {
				return
			}
			defer func() { _ = f.release(ownedRemote) }()
			copyStream := func(destination, source *endpointConn) {
				if _, copyErr := io.Copy(destination, source); copyErr != nil {
					_ = f.release(destination)
					_ = f.release(source)
					return
				}
				closeWrite(destination)
			}
			var copies sync.WaitGroup
			copies.Add(1)
			go func() { defer copies.Done(); copyStream(ownedRemote, ownedLocal) }()
			copyStream(ownedLocal, ownedRemote)
			copies.Wait()
		}()
	}
}

func closeWrite(conn net.Conn) {
	if half, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = half.CloseWrite()
	}
}

func (f *endpointForwarder) receiveUDP() {
	defer f.workers.Done()
	buffer := make([]byte, 65535)
	for {
		n, peer, err := f.udp.ReadFrom(buffer)
		if err != nil {
			return
		}
		key := peer.String()
		f.mu.Lock()
		remote := f.peers[key]
		f.mu.Unlock()
		if remote == nil {
			dialed, err := protected_dialer.DialUDPConnWithProtect(f.ctx, f.cache, "udp", f.address)
			if err != nil {
				if f.ctx.Err() == nil {
					log.Warnf("trusttunnel", "endpoint UDP dial failed destination=%s: %v", f.address, err)
				}
				continue
			}
			remote = f.track(dialed)
			if remote == nil {
				return
			}
			f.mu.Lock()
			f.peers[key] = remote
			f.mu.Unlock()
			f.workers.Add(1)
			go f.returnUDP(key, peer, remote)
		}
		if _, err := remote.Write(buffer[:n]); err != nil && f.ctx.Err() == nil {
			log.Warnf("trusttunnel", "endpoint UDP write failed destination=%s: %v", f.address, err)
		}
	}
}

func (f *endpointForwarder) returnUDP(key string, peer net.Addr, remote *endpointConn) {
	defer f.workers.Done()
	defer func() { f.mu.Lock(); delete(f.peers, key); f.mu.Unlock(); _ = f.release(remote) }()
	buffer := make([]byte, 65535)
	for {
		n, err := remote.Read(buffer)
		if err != nil {
			return
		}
		if _, err = f.udp.WriteTo(buffer[:n], peer); err != nil {
			return
		}
	}
}

func (f *endpointForwarder) Close() error {
	f.cancel()
	f.mu.Lock()
	f.closed = true
	connections := make([]*endpointConn, 0, len(f.connections))
	for conn := range f.connections {
		connections = append(connections, conn)
	}
	f.mu.Unlock()
	var result error
	for _, close := range []func() error{f.tcp.Close, f.udp.Close} {
		if err := close(); err != nil && !errors.Is(err, net.ErrClosed) {
			result = errors.Join(result, err)
		}
	}
	for _, conn := range connections {
		if err := f.release(conn); err != nil {
			result = errors.Join(result, err)
		}
	}
	if result != nil {
		return fmt.Errorf("close protected endpoint forwarder: %w", result)
	}
	f.workers.Wait()
	// A dial may hand back a socket after the first snapshot. Failed late
	// closes remain tracked until cleanup can complete safely.
	f.mu.Lock()
	connections = connections[:0]
	for conn := range f.connections {
		connections = append(connections, conn)
	}
	f.mu.Unlock()
	for _, conn := range connections {
		result = errors.Join(result, f.release(conn))
	}
	if result != nil {
		return fmt.Errorf("close late protected endpoint socket: %w", result)
	}
	return nil
}
