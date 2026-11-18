package tunnel

import (
	"context"
	"net"
	"net/netip"
	"sync"

	"core/log"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	"github.com/xjasonlyu/tun2socks/v2/proxy"
	"github.com/xjasonlyu/tun2socks/v2/proxy/proto"
)

// switchingProxy belongs to the packet stack, not a protocol. TCP waits under
// the stack's existing dial context; UDP has no queue and is dropped while no
// attempt is available. Retired targets seal their own flow owner, fencing a
// dial that raced removal without holding a lock over network IO.
type switchingProxy struct {
	mu      sync.Mutex
	target  proxy.Proxy
	changed chan struct{}
	closed  bool
}

func newSwitchingProxy() *switchingProxy { return &switchingProxy{changed: make(chan struct{})} }

func (p *switchingProxy) set(target proxy.Proxy) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.target = target
	close(p.changed)
	p.changed = make(chan struct{})
}

func (p *switchingProxy) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed, p.target = true, nil
	close(p.changed)
}

func (p *switchingProxy) DialContext(ctx context.Context, metadata *M.Metadata) (net.Conn, error) {
	for {
		p.mu.Lock()
		target, changed, closed := p.target, p.changed, p.closed
		p.mu.Unlock()
		if closed {
			return nil, net.ErrClosed
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if target != nil {
			return target.DialContext(ctx, metadata)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

func (p *switchingProxy) DialUDP(metadata *M.Metadata) (net.PacketConn, error) {
	p.mu.Lock()
	target := p.target
	p.mu.Unlock()
	if target == nil {
		return nil, net.ErrClosed
	}
	return target.DialUDP(metadata)
}

// RejectTCP lets the packet stack refuse an IPv6 connection that the current
// attempt's transport cannot carry, so the application falls back to IPv4.
func (p *switchingProxy) RejectTCP(dst netip.Addr) bool {
	if !dst.Is6() || dst.Is4In6() {
		return false
	}
	p.mu.Lock()
	target, _ := p.target.(*DobbyProxy)
	p.mu.Unlock()
	if target == nil || target.bypass == nil || target.bypass.ipv6Unavailable == nil {
		return false
	}
	if !target.bypass.ipv6Unavailable() || target.bypass.IsBypass(&M.Metadata{DstIP: dst}) {
		return false
	}
	// Detach and Attach may have replaced the attempt while its policy ran; a
	// refusal from a retired attempt must not reset a SYN the new one carries.
	p.mu.Lock()
	current := p.target
	p.mu.Unlock()
	if current != proxy.Proxy(target) {
		return p.RejectTCP(dst)
	}
	log.Debugf(Category, "[Router] IPv6 refused, transport has no IPv6 dst=%s", dst)
	return true
}

func (p *switchingProxy) Addr() string       { return "vpn-network" }
func (p *switchingProxy) Proto() proto.Proto { return proto.Socks5 }
