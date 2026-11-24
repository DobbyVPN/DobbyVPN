package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sync"
	"time"

	"core/log"

	"github.com/xjasonlyu/tun2socks/v2/core/adapter"
	"github.com/xjasonlyu/tun2socks/v2/dialer"
	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	"github.com/xjasonlyu/tun2socks/v2/proxy"
	"github.com/xjasonlyu/tun2socks/v2/transport/socks5"
)

func (p *switchingProxy) HandleTCP(conn adapter.TCPConn) {
	// The explicit stack never configures tun2socks' default, rejecting relay.
	// This relay dials through the active attempt selected by the stack owner.
	select {
	case p.tcpRelay.TCPIn() <- conn:
		return
	case <-p.done:
		if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			log.Errorf(Category, "[Router] stopped TCP origin close failed local=%s remote=%s err=%v", conn.LocalAddr(), conn.RemoteAddr(), err)
		}
	}
}

// socksTCPProxy negotiates the local SOCKS transport under the attempt deadline.
// Successful calls return the original socket, preserving TCP half-close.
type socksTCPProxy struct {
	proxy.Proxy
	address string
	user    *socks5.User
	owner   *flowOwner
}

func newSocksTCPProxy(outbound proxy.Proxy, address string, owner *flowOwner) (proxy.Proxy, error) {
	if _, ok := outbound.(*proxy.Socks5); !ok {
		return nil, fmt.Errorf("local tunnel outbound is not SOCKS5: %T", outbound)
	}
	endpoint, err := url.Parse("socks5://" + address)
	if err != nil {
		return nil, fmt.Errorf("parse local SOCKS endpoint: %w", err)
	}
	p := &socksTCPProxy{Proxy: outbound, address: endpoint.Host, owner: owner}
	if endpoint.User != nil && endpoint.User.Username() != "" {
		password, _ := endpoint.User.Password()
		p.user = &socks5.User{Username: endpoint.User.Username(), Password: password}
	}
	return p, nil
}

func (p *socksTCPProxy) DialContext(ctx context.Context, metadata *M.Metadata) (net.Conn, error) {
	target := socks5.SerializeAddr("", metadata.DstIP, metadata.DstPort)
	conn, _, err := p.negotiate(ctx, socks5.CmdConnect, target)
	return conn, err
}

func (p *socksTCPProxy) negotiate(ctx context.Context, command socks5.Command, target socks5.Addr) (net.Conn, socks5.Addr, error) {
	started := time.Now()
	conn, err := dialer.DialContext(ctx, "tcp", p.address)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to local SOCKS proxy: %w", err)
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		if err := tcp.SetKeepAlive(true); err != nil {
			log.Debugf(Category, "[Router] local SOCKS TCP keepalive failed address=%s err=%v", p.address, err)
		}
		if err := tcp.SetKeepAlivePeriod(30 * time.Second); err != nil {
			log.Debugf(Category, "[Router] local SOCKS TCP keepalive period failed address=%s err=%v", p.address, err)
		}
	}
	// ClientHandshake only accepts io.ReadWriter. Close its raw socket when
	// cancellation arrives, then join or unregister the callback before any
	// successful handoff so later caller cancellation cannot close a live flow.
	lease := &pendingSOCKSConn{Conn: conn, owner: p.owner}
	done := make(chan struct{})
	var cancellationCloseErr error
	stop := context.AfterFunc(ctx, func() {
		cancellationCloseErr = lease.Close()
		close(done)
	})
	bound, handshakeErr := socks5.ClientHandshake(conn, target, command, p.user)
	if !stop() {
		<-done
	}
	if ctx.Err() != nil || handshakeErr != nil {
		closeErr := lease.Close()
		err := errors.Join(ctx.Err(), handshakeErr, cancellationCloseErr, closeErr)
		log.Debugf(Category, "[Router] local SOCKS handshake ended address=%s dest=%s elapsed=%s ctxErr=%v err=%v", p.address, target.String(), time.Since(started), ctx.Err(), err)
		return nil, nil, err
	}
	return conn, bound, nil
}

// A failed raw socket close remains an engine-owned lease for Stop to retry.
// This lease never owns a completed-flow count or wraps the successful result.
type pendingSOCKSConn struct {
	net.Conn
	owner *flowOwner
	mu    sync.Mutex
}

func (c *pendingSOCKSConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.Conn.Close()
	if errors.Is(err, net.ErrClosed) {
		err = nil
	}
	c.owner.release(c, err)
	if err != nil {
		log.Errorf(Category, "[Router] pending local SOCKS TCP close failed remote=%s err=%v", c.RemoteAddr(), err)
	}
	return err
}
