package tunnel

import (
	"errors"
	"net"
	"sync"
	"time"

	"core/log"

	"github.com/xjasonlyu/tun2socks/v2/core/adapter"
	stackTunnel "github.com/xjasonlyu/tun2socks/v2/tunnel"
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
	}
	c.owner.release(c, err)
	if err != nil {
		log.Errorf(Category, "[Router] UDP origin close failed generation=%d local=%s remote=%s err=%v", c.generation, local, remote, err)
	} else {
		log.Debugf(Category, "[Router] UDP origin closed generation=%d local=%s remote=%s lifetime=%s", c.generation, local, remote, time.Since(c.started))
	}
	return err
}

func (p *switchingProxy) HandleTCP(conn adapter.TCPConn) {
	stackTunnel.T().HandleTCP(conn)
}

func (p *switchingProxy) HandleUDP(conn adapter.UDPConn) {
	p.mu.Lock()
	target, _ := p.target.(*DobbyProxy)
	var admissionErr error
	if target != nil && target.udpRelay != nil {
		// Register pending admission before Detach can fence the router. Its
		// cleanup then includes origin closure even if dispatch races retirement.
		admissionErr = target.flows.beginDial()
	}
	p.mu.Unlock()
	if target == nil || target.udpRelay == nil || admissionErr != nil {
		if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			log.Errorf(Category, "[Router] unavailable UDP origin close failed local=%s remote=%s err=%v", conn.LocalAddr(), conn.RemoteAddr(), err)
		}
		return
	}
	defer target.flows.endDial()
	origin := &trackedUDPOrigin{UDPConn: conn, owner: &target.flows, generation: target.generation, started: time.Now()}
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
