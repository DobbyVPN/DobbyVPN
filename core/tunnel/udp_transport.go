package tunnel

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"core/log"

	"github.com/xjasonlyu/tun2socks/v2/core/adapter"
)

// The packet stack keeps UDP endpoints until both relay directions finish.
// Closing only the outbound socket leaves its input endpoint alive, so a new
// application socket reusing that tuple can feed the retired relay. Retain the
// input endpoint with the same attempt owner and close both during Detach.
type trackedUDPOrigin struct {
	adapter.UDPConn
	owner      *flowOwner
	generation uint64
	started    time.Time
	mu         sync.Mutex
	closed     bool
	release    func()
	lastIO     atomic.Int64
}

var _ adapter.TransportHandler = (*switchingProxy)(nil)

func (c *trackedUDPOrigin) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	local, remote := c.LocalAddr(), c.RemoteAddr()
	err := c.UDPConn.Close()
	if err == nil || errors.Is(err, net.ErrClosed) {
		c.closed = true
		err = nil
		if c.release != nil {
			c.release()
		}
	}
	c.owner.release(c, err)
	if err != nil {
		log.Errorf(Category, "[Router] UDP origin close failed generation=%d local=%s remote=%s err=%v", c.generation, local, remote, err)
	} else {
		log.Debugf(Category, "[Router] UDP origin closed generation=%d local=%s remote=%s lifetime=%s", c.generation, local, remote, time.Since(c.started))
	}
	return err
}

func (c *trackedUDPOrigin) ReadFrom(b []byte) (int, net.Addr, error) {
	n, addr, err := c.UDPConn.ReadFrom(b)
	if n > 0 && c.release != nil {
		c.lastIO.Store(time.Since(flowClock).Nanoseconds())
	}
	return n, addr, err
}

func (c *trackedUDPOrigin) WriteTo(b []byte, addr net.Addr) (int, error) {
	n, err := c.UDPConn.WriteTo(b, addr)
	if n > 0 && c.release != nil {
		c.lastIO.Store(time.Since(flowClock).Nanoseconds())
	}
	return n, err
}

func (p *switchingProxy) HandleUDP(conn adapter.UDPConn) {
	p.mu.Lock()
	target, _ := p.target.(*DobbyProxy)
	var admissionErr error
	var release func()
	available := target != nil && target.udpRelay != nil
	if available && p.udpLimit > 0 {
		if p.udpPaused || target.udpOrigins.Load() >= p.udpLimit {
			target.udpRefused.Add(1)
			admissionErr = net.ErrClosed
		} else {
			target.udpOrigins.Add(1)
			release = func() { target.udpOrigins.Add(-1) }
		}
	}
	// Register pending admission before Detach can fence the router so its
	// cleanup includes origin closure when dispatch races retirement.
	if available && admissionErr == nil {
		admissionErr = target.flows.beginDial()
	}
	p.mu.Unlock()
	if !available || admissionErr != nil {
		if release != nil {
			release()
		}
		if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			log.Errorf(Category, "[Router] unavailable UDP origin close failed local=%s remote=%s err=%v", conn.LocalAddr(), conn.RemoteAddr(), err)
		}
		return
	}
	defer target.flows.endDial()
	origin := &trackedUDPOrigin{UDPConn: conn, owner: &target.flows, generation: target.generation, started: time.Now(), release: release}
	if !target.flows.track(origin) {
		_ = origin.Close() // Retains a failed close in the sealed owner.
		return
	}
	log.Debugf(Category, "[Router] UDP origin admitted generation=%d local=%s remote=%s", target.generation, conn.LocalAddr(), conn.RemoteAddr())
	select {
	case target.udpRelay.UDPIn() <- origin:
	case <-target.flows.done():
		_ = origin.Close()
	}
}
