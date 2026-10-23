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

	"github.com/xjasonlyu/tun2socks/v2/dialer"
	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	"github.com/xjasonlyu/tun2socks/v2/proxy"
	"github.com/xjasonlyu/tun2socks/v2/transport/socks5"
)

// socksTCPProxy retains the existing UDP implementation and makes the local
// TCP SOCKS handshake obey its caller and engine generation cancellation.
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
	started := time.Now()
	conn, err := dialer.DialContext(ctx, "tcp", p.address)
	if err != nil {
		return nil, fmt.Errorf("connect to local SOCKS proxy: %w", err)
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
	address := socks5.SerializeAddr("", metadata.DstIP, metadata.DstPort)
	_, handshakeErr := socks5.ClientHandshake(conn, address, socks5.CmdConnect, p.user)
	if !stop() {
		<-done
	}
	if ctx.Err() != nil || handshakeErr != nil {
		closeErr := lease.Close()
		err := errors.Join(ctx.Err(), handshakeErr, cancellationCloseErr, closeErr)
		log.Debugf(Category, "[Router] local SOCKS TCP handshake ended address=%s dest=%s elapsed=%s ctxErr=%v err=%v", p.address, metadata.DestinationAddress(), time.Since(started), ctx.Err(), err)
		return nil, err
	}
	return conn, nil
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
