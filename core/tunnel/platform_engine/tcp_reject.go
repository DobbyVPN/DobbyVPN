package platform_engine

import (
	"net/netip"
	"sync"
	"time"

	"github.com/xjasonlyu/tun2socks/v2/core"
	stackAdapter "github.com/xjasonlyu/tun2socks/v2/core/adapter"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/waiter"
)

// tcpRejecter refuses a new connection before the packet stack completes the
// handshake. The application then sees "connection refused" and falls back to
// another address instead of a connection that succeeds locally and dies.
type tcpRejecter interface {
	RejectTCP(dst netip.Addr) bool
}

// Same values as the tun2socks forwarder this one replaces.
const (
	tcpMaxConnAttempts   = 2 << 10
	tcpKeepaliveCount    = 9
	tcpKeepaliveIdle     = 60 * time.Second
	tcpKeepaliveInterval = 30 * time.Second
)

// createStack is core.CreateStack with a TCP forwarder that consults the
// handler before answering a SYN. The device is attached only after the
// forwarder is installed, so no packet reaches the default one.
func createStack(dev stack.LinkEndpoint, handler stackAdapter.TransportHandler, admission *tcpAdmission) (*stack.Stack, error) {
	rejecter, ok := handler.(tcpRejecter)
	if !ok && admission == nil {
		return core.CreateStack(&core.Config{LinkEndpoint: dev, TransportHandler: handler})
	}
	gate := &attachGate{LinkEndpoint: dev}
	s, err := core.CreateStack(&core.Config{LinkEndpoint: gate, TransportHandler: handler, Options: tcpStackOptions})
	if err != nil {
		return s, err
	}
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, newTCPForwarder(s, handler, rejecter, admission).HandlePacket)
	gate.open()
	return s, nil
}

func newTCPForwarder(s *stack.Stack, handler stackAdapter.TransportHandler, rejecter tcpRejecter, admission *tcpAdmission) *tcp.Forwarder {
	return tcp.NewForwarder(s, 0, tcpMaxConnAttempts, func(r *tcp.ForwarderRequest) {
		id := r.ID()
		if dst, ok := netip.AddrFromSlice(id.LocalAddress.AsSlice()); ok && rejecter != nil && rejecter.RejectTCP(dst) {
			r.Complete(true)
			return
		}
		var release func()
		if admission != nil {
			release = admission.acquire(64, 64, 10*time.Second)
			if release == nil {
				r.Complete(true)
				return
			}
		}
		var wq waiter.Queue
		ep, err := r.CreateEndpoint(&wq)
		if err != nil {
			if release != nil {
				release()
			}
			r.Complete(true)
			return
		}
		defer r.Complete(false)
		setTCPSocketOptions(s, ep)
		handler.HandleTCP(&tcpConn{TCPConn: gonet.NewTCPConn(&wq, ep), id: id, release: release})
	})
}

func setTCPSocketOptions(s *stack.Stack, ep tcpip.Endpoint) {
	ep.SocketOptions().SetKeepAlive(true)
	idle := tcpip.KeepaliveIdleOption(tcpKeepaliveIdle)
	_ = ep.SetSockOpt(&idle)
	interval := tcpip.KeepaliveIntervalOption(tcpKeepaliveInterval)
	_ = ep.SetSockOpt(&interval)
	_ = ep.SetSockOptInt(tcpip.KeepaliveCountOption, tcpKeepaliveCount)
	var send tcpip.TCPSendBufferSizeRangeOption
	if err := s.TransportProtocolOption(header.TCPProtocolNumber, &send); err == nil {
		ep.SocketOptions().SetSendBufferSize(int64(send.Default), false)
	}
	var receive tcpip.TCPReceiveBufferSizeRangeOption
	if err := s.TransportProtocolOption(header.TCPProtocolNumber, &receive); err == nil {
		ep.SocketOptions().SetReceiveBufferSize(int64(receive.Default), false)
	}
}

type tcpConn struct {
	*gonet.TCPConn
	id      stack.TransportEndpointID
	release func()
}

func (c *tcpConn) Close() error {
	err := c.TCPConn.Close()
	if c.release != nil {
		c.release()
	}
	return err
}

func (c *tcpConn) ID() *stack.TransportEndpointID { return &c.id }

// attachGate holds the device back until open, so the stack can be fully
// configured before the first packet arrives.
type attachGate struct {
	stack.LinkEndpoint
	mu         sync.Mutex
	dispatcher stack.NetworkDispatcher
	opened     bool
}

func (g *attachGate) Attach(dispatcher stack.NetworkDispatcher) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.dispatcher = dispatcher
	if g.opened || dispatcher == nil {
		g.LinkEndpoint.Attach(dispatcher)
	}
}

func (g *attachGate) open() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.opened = true
	g.LinkEndpoint.Attach(g.dispatcher)
}
