package internal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"core/log"
	"core/xray/common"
)

// Xray transports may cache sockets beyond Instance.Close (notably QUIC).
// The attempt therefore owns every socket returned by its system dialer.
type transportSockets struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	closing bool
	pending int
	settled chan struct{}
	owned   map[*transportSocket]error
}

func newTransportSockets() *transportSockets {
	ctx, cancel := context.WithCancel(context.Background())
	return &transportSockets{ctx: ctx, cancel: cancel, owned: make(map[*transportSocket]error)}
}

func (s *transportSockets) begin() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return net.ErrClosed
	}
	if s.pending == 0 {
		s.settled = make(chan struct{})
	}
	s.pending++
	return nil
}

func (s *transportSockets) end() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending--
	if s.pending == 0 {
		close(s.settled)
	}
}

func (s *transportSockets) track(raw io.Closer) (*transportSocket, error) {
	socket := &transportSocket{raw: raw, owner: s}
	s.mu.Lock()
	s.owned[socket] = nil
	closing := s.closing
	s.mu.Unlock()
	if closing {
		return nil, errors.Join(net.ErrClosed, socket.Close())
	}
	return socket, nil
}

func (s *transportSockets) close(ctx context.Context) error {
	s.mu.Lock()
	s.closing = true
	s.cancel()
	sockets := make([]*transportSocket, 0, len(s.owned))
	for socket := range s.owned {
		sockets = append(sockets, socket)
	}
	pending, settled := s.pending, s.settled
	s.mu.Unlock()
	log.Debugf(common.Category, "retiring transport sockets open=%d pending_dials=%d", len(sockets), pending)
	for _, socket := range sockets {
		// Close retains any failed handle for a subsequent cleanup attempt.
		_ = socket.Close()
	}
	if pending != 0 {
		select {
		case <-settled:
		case <-ctx.Done():
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var result error
	if s.pending != 0 {
		result = fmt.Errorf("xray transport cleanup has %d unresolved dials: %w", s.pending, ctx.Err())
	}
	for _, err := range s.owned {
		if err == nil {
			err = errors.New("xray transport socket remains open")
		}
		result = errors.Join(result, err)
	}
	if result != nil {
		log.Errorf(common.Category, "transport cleanup incomplete open=%d pending_dials=%d err=%v", len(s.owned), s.pending, result)
	} else {
		log.Debugf(common.Category, "transport sockets retired open=0 pending_dials=0")
	}
	return result
}

type transportSocket struct {
	mu     sync.Mutex
	raw    io.Closer
	owner  *transportSockets
	closed bool
}

func (s *transportSocket) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	err := s.raw.Close()
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	if err != nil && !errors.Is(err, net.ErrClosed) {
		s.owner.owned[s] = err
		return err
	}
	s.closed = true
	delete(s.owner.owned, s)
	return nil
}

// Embedding the native sockets preserves half-close, RawConn and UDP message
// APIs used by QUIC, including ancillary ECN and packet-info handling.
type ownedTCPConn struct {
	*net.TCPConn
	socket *transportSocket
}

func (c *ownedTCPConn) Close() error { return c.socket.Close() }

type ownedUDPConn struct {
	*net.UDPConn
	socket *transportSocket
}

func (c *ownedUDPConn) Close() error { return c.socket.Close() }
