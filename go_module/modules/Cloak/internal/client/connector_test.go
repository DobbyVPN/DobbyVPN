package client

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type cancellationDialer struct{}

func (cancellationDialer) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestMakeSessionStopsWhenDialIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	transport := &testTransport{closed: make(chan struct{})}
	go func() {
		_, err := makeSession(ctx, RemoteConnConfig{NumConn: 1}, AuthInfo{}, cancellationDialer{}, func(TransportConfig) Transport {
			return transport
		})
		done <- err
	}()
	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("MakeSession error=%v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("MakeSession did not stop after cancellation")
	}
}

type pipeDialer struct {
	mu    sync.Mutex
	peers []net.Conn
}

func (d *pipeDialer) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	clientConn, peer := net.Pipe()
	d.mu.Lock()
	d.peers = append(d.peers, peer)
	d.mu.Unlock()
	return clientConn, nil
}

func (d *pipeDialer) closePeers() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, peer := range d.peers {
		_ = peer.Close()
	}
}

type testTransport struct {
	raw       net.Conn
	started   chan<- struct{}
	succeed   bool
	closed    chan struct{}
	closeOnce sync.Once
}

func (t *testTransport) Handshake(raw net.Conn, _ AuthInfo) ([32]byte, error) {
	t.raw = raw
	if t.started != nil {
		t.started <- struct{}{}
	}
	if t.succeed {
		return [32]byte{1}, nil
	}
	var b [1]byte
	_, err := raw.Read(b[:])
	return [32]byte{}, err
}

func (t *testTransport) Read(p []byte) (int, error)         { return t.raw.Read(p) }
func (t *testTransport) Write(p []byte) (int, error)        { return t.raw.Write(p) }
func (t *testTransport) LocalAddr() net.Addr                { return t.raw.LocalAddr() }
func (t *testTransport) RemoteAddr() net.Addr               { return t.raw.RemoteAddr() }
func (t *testTransport) SetDeadline(v time.Time) error      { return t.raw.SetDeadline(v) }
func (t *testTransport) SetReadDeadline(v time.Time) error  { return t.raw.SetReadDeadline(v) }
func (t *testTransport) SetWriteDeadline(v time.Time) error { return t.raw.SetWriteDeadline(v) }
func (t *testTransport) Close() error {
	t.closeOnce.Do(func() { close(t.closed) })
	if t.raw != nil {
		return t.raw.Close()
	}
	return nil
}

func TestMakeSessionCancellationClosesBlockedHandshake(t *testing.T) {
	dialer := &pipeDialer{}
	defer dialer.closePeers()
	started := make(chan struct{}, 1)
	transport := &testTransport{started: started, closed: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := makeSession(ctx, RemoteConnConfig{NumConn: 1}, AuthInfo{}, dialer, func(TransportConfig) Transport { return transport })
		done <- err
	}()
	<-started
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("makeSession error=%v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("makeSession did not stop a blocked handshake")
	}
	select {
	case <-transport.closed:
	case <-time.After(time.Second):
		t.Fatal("blocked transport was not closed")
	}
}

func TestMakeSessionCancellationClosesPartialConnections(t *testing.T) {
	dialer := &pipeDialer{}
	defer dialer.closePeers()
	started := make(chan struct{}, 2)
	first := &testTransport{started: started, succeed: true, closed: make(chan struct{})}
	second := &testTransport{started: started, closed: make(chan struct{})}
	var created atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := makeSession(ctx, RemoteConnConfig{NumConn: 2}, AuthInfo{}, dialer, func(TransportConfig) Transport {
			if created.Add(1) == 1 {
				return first
			}
			return second
		})
		done <- err
	}()
	<-started
	<-started
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("makeSession error=%v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("makeSession did not stop after partial connection setup")
	}
	for name, closed := range map[string]<-chan struct{}{"successful": first.closed, "blocked": second.closed} {
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatalf("%s transport was not closed", name)
		}
	}
}

func TestMakeSessionInternalFailureCancelsBlockedWorker(t *testing.T) {
	dialer := &pipeDialer{}
	defer dialer.closePeers()
	started := make(chan struct{}, 1)
	blockedWorkerStarted := make(chan struct{})
	blocked := &testTransport{started: started, closed: make(chan struct{})}
	var created atomic.Int32
	done := make(chan error, 1)
	go func() {
		_, err := makeSession(context.Background(), RemoteConnConfig{NumConn: 2}, AuthInfo{}, dialer, func(TransportConfig) Transport {
			if created.Add(1) == 1 {
				return blocked
			}
			<-started
			close(blockedWorkerStarted)
			return nil
		})
		done <- err
	}()
	<-blockedWorkerStarted

	select {
	case err := <-done:
		if err == nil || err.Error() != "transport factory returned nil" {
			t.Fatalf("makeSession error=%v, want transport factory failure", err)
		}
	case <-time.After(time.Second):
		t.Fatal("internal worker failure did not cancel blocked handshake")
	}
	select {
	case <-blocked.closed:
	case <-time.After(time.Second):
		t.Fatal("internally canceled blocked transport was not closed")
	}
}
