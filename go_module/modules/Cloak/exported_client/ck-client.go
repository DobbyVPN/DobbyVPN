//go:build go1.11
// +build go1.11

package exported_client

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"go_module/log"
	"net"
	"sync"
	"time"

	"github.com/cbeuw/Cloak/internal/client"
	"github.com/cbeuw/Cloak/internal/common"
	mux "github.com/cbeuw/Cloak/internal/multiplex"
)

type CkClient struct {
	opMu      sync.Mutex
	mu        sync.Mutex
	connected bool
	epoch     uint64
	config    client.RawConfig
	sessions  map[*mux.Session]struct{}
	listener  net.Listener
	udpConn   *net.UDPConn
	routeDone chan struct{}
	routeStop context.CancelFunc
	dialer    common.Dialer
}

type Config client.RawConfig

func NewCkClient(config Config) *CkClient {
	return &CkClient{config: client.RawConfig(config), sessions: make(map[*mux.Session]struct{})}
}

func (c *CkClient) Connect() (returnErr error) {
	if c == nil {
		return errors.New("ck-client is not initialized")
	}

	c.opMu.Lock()
	defer c.opMu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			log.Debugf("ck-client", "ck-client Connect: recovered from panic: %v", r)
			returnErr = errors.Join(fmt.Errorf("panic in Connect: %v", r), c.disconnectCurrent())
		}
	}()
	c.mu.Lock()
	if c.connected {
		c.mu.Unlock()
		return errors.New("ck-client is already connected")
	}
	if c.routeDone != nil {
		select {
		case <-c.routeDone:
			c.routeDone, c.routeStop = nil, nil
		default:
			c.mu.Unlock()
			return errors.New("previous Cloak routing goroutine is still shutting down")
		}
	}
	c.mu.Unlock()

	localConfig, remoteConfig, authInfo, err := c.config.ProcessRawConfig(common.RealWorldState)
	if err != nil {
		return fmt.Errorf("failed to process cloak config: %w", err)
	}
	log.Debugf("ck-client", "ck-client connected")

	d := c.dialer
	if d == nil {
		d = &net.Dialer{Control: protector, KeepAlive: remoteConfig.KeepAlive}
	}
	var network string
	if authInfo.Unordered {
		network = "UDP"
	} else {
		network = "TCP"
	}
	log.Debugf("ck-client", "ck-client: Listening on %v %v for %v client", network, localConfig.LocalAddr, authInfo.ProxyMethod)
	done := make(chan struct{})
	ready := make(chan error, 1)
	routeCtx, routeStop := context.WithCancel(context.Background())
	c.mu.Lock()
	c.epoch++
	epoch := c.epoch
	c.connected, c.routeDone, c.routeStop = true, done, routeStop
	c.mu.Unlock()
	seshMaker := func() (*mux.Session, error) {
		remote, auth := nextClientSession(localConfig, remoteConfig, authInfo)
		session, err := client.MakeSession(routeCtx, remote, auth, d)
		if err != nil {
			return nil, err
		}
		return c.registerSession(epoch, session), nil
	}

	go func() {
		// Signal completion so Disconnect() can wait for the routing goroutine to exit.
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				log.Debugf("ck-client", "ck-client: recovered from panic from: %v", r)
			}
		}()
		if authInfo.Unordered {
			udpAddr, _ := net.ResolveUDPAddr("udp", localConfig.LocalAddr)
			conn, err := net.ListenUDP("udp", udpAddr)
			if err != nil {
				log.Debugf("ck-client", "ck-client: goroutines: err %v\n", err)
				ready <- fmt.Errorf("failed to listen on UDP %s: %w", localConfig.LocalAddr, err)
				return
			}

			if !c.publishUDPConn(epoch, conn) {
				_ = conn.Close()
				ready <- errors.New("Cloak connection was canceled before UDP listener startup")
				return
			}

			log.Debugf("ck-client", "ck-client: start listening on UDP %v for %v client", localConfig.LocalAddr, authInfo.ProxyMethod)
			ready <- nil
			client.RouteUDP(routeCtx, func() (*net.UDPConn, error) { return conn, nil }, localConfig.Timeout, remoteConfig.Singleplex, seshMaker)
			log.Debugf("ck-client", "ck-client: stop listening on UDP %v for %v client", localConfig.LocalAddr, authInfo.ProxyMethod)
		} else {
			baseListener, err := net.Listen("tcp", localConfig.LocalAddr)
			if err != nil {
				log.Debugf("ck-client", "ck-client: goroutines: err %v\n", err)
				ready <- fmt.Errorf("failed to listen on TCP %s: %w", localConfig.LocalAddr, err)
				return
			}

			if !c.publishTCPListener(epoch, baseListener) {
				_ = baseListener.Close()
				ready <- errors.New("Cloak connection was canceled before TCP listener startup")
				return
			}

			log.Debugf("ck-client", "ck-client: start listening on TCP %v for %v client", localConfig.LocalAddr, authInfo.ProxyMethod)
			ready <- nil
			client.RouteTCP(routeCtx, baseListener, localConfig.Timeout, remoteConfig.Singleplex, seshMaker)
			log.Debugf("ck-client", "ck-client: stop listening on TCP %v for %v client", localConfig.LocalAddr, authInfo.ProxyMethod)
		}
	}()

	select {
	case err := <-ready:
		if err != nil {
			return errors.Join(err, c.disconnectCurrent())
		}
	case <-time.After(2 * time.Second):
		return errors.Join(fmt.Errorf("timed out waiting for Cloak listener on %s", localConfig.LocalAddr), c.disconnectCurrent())
	}

	return nil
}

func nextClientSession(
	local client.LocalConnConfig,
	remote client.RemoteConnConfig,
	auth client.AuthInfo,
) (client.RemoteConnConfig, client.AuthInfo) {
	randByte := make([]byte, 1)
	common.RandRead(auth.WorldState.Rand, randByte)
	auth.MockDomain = local.MockDomainList[int(randByte[0])%len(local.MockDomainList)]

	// Session IDs are scoped to the configured user UID. Generating one here
	// matches Cloak's ordinary client path instead of forcing admin parameters.
	quad := make([]byte, 4)
	common.RandRead(auth.WorldState.Rand, quad)
	auth.SessionId = binary.BigEndian.Uint32(quad)
	return remote, auth
}

func (c *CkClient) publishTCPListener(epoch uint64, listener net.Listener) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.connected || c.epoch != epoch {
		return false
	}
	c.listener = listener
	return true
}

func (c *CkClient) publishUDPConn(epoch uint64, conn *net.UDPConn) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.connected || c.epoch != epoch {
		return false
	}
	c.udpConn = conn
	return true
}

func (c *CkClient) registerSession(epoch uint64, session *mux.Session) *mux.Session {
	c.mu.Lock()
	if c.connected && c.epoch == epoch {
		if c.sessions == nil {
			c.sessions = make(map[*mux.Session]struct{})
		}
		for existing := range c.sessions {
			if existing.IsClosed() {
				delete(c.sessions, existing)
			}
		}
		c.sessions[session] = struct{}{}
		c.mu.Unlock()
		return session
	}
	c.mu.Unlock()

	// A listener can finish creating a session after Disconnect has started.
	// Close that late session immediately instead of publishing it as active.
	if err := session.Close(); err != nil {
		log.Debugf("ck-client", "ck-client: late session close returned: %v", err)
	}
	return session
}

func (c *CkClient) Disconnect() error {
	if c == nil {
		return errors.New("ck-client is not initialized")
	}
	c.opMu.Lock()
	defer c.opMu.Unlock()
	return c.disconnectCurrent()
}

func (c *CkClient) disconnectCurrent() error {
	c.mu.Lock()
	if !c.connected && c.listener == nil && c.udpConn == nil && len(c.sessions) == 0 {
		done := c.routeDone
		stop := c.routeStop
		c.mu.Unlock()
		if stop != nil {
			stop()
		}
		err := waitForRouteDone(done)
		if err == nil && done != nil {
			c.mu.Lock()
			if c.routeDone == done {
				c.routeDone, c.routeStop = nil, nil
			}
			c.mu.Unlock()
		}
		log.Debugf("ck-client", "ck-client: already disconnected")
		return err
	}

	log.Debugf("ck-client", "ck-client: initiating disconnect...")
	c.connected = false
	listener, udpConn, done, stop := c.listener, c.udpConn, c.routeDone, c.routeStop
	sessions := make([]*mux.Session, 0, len(c.sessions))
	for session := range c.sessions {
		sessions = append(sessions, session)
	}
	c.listener, c.udpConn, c.sessions = nil, nil, make(map[*mux.Session]struct{})
	c.mu.Unlock()
	if stop != nil {
		stop()
	}

	if listener != nil {
		addr := listener.Addr().String()
		if err := listener.Close(); err != nil {
			log.Debugf("ck-client", "ck-client: error closing TCP listener %v: %v", addr, err)
		} else {
			log.Debugf("ck-client", "ck-client: TCP listener %v closed", addr)
		}
	}

	if udpConn != nil {
		addr := udpConn.LocalAddr().String()
		if err := udpConn.Close(); err != nil {
			log.Debugf("ck-client", "ck-client: error closing UDP conn %v: %v", addr, err)
		} else {
			log.Debugf("ck-client", "ck-client: UDP listener %v closed", addr)
		}
	}

	for _, session := range sessions {
		log.Debugf("ck-client", "ck-client: closing session...")
		if err := session.Close(); err != nil {
			log.Debugf("ck-client", "ck-client: session close returned: %v", err)
		}
		log.Debugf("ck-client", "ck-client: session closed")
	}

	// Do not report completion until the routing goroutine has exited.
	waitErr := waitForRouteDone(done)
	c.mu.Lock()
	if waitErr == nil && c.routeDone == done {
		c.routeDone, c.routeStop = nil, nil
	}
	c.mu.Unlock()

	log.Debugf("ck-client", "ck-client: fully disconnected")
	return waitErr
}

func waitForRouteDone(done <-chan struct{}) error {
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-time.After(2 * time.Second):
		return errors.New("Cloak routing goroutine did not exit within timeout")
	}
}

func (c *CkClient) Refresh() error {
	if err := c.Disconnect(); err != nil { // TODO: handle error with more detail
		return fmt.Errorf("failed to refresh cloak client: disconnect failed: %w", err)
	}

	if err := c.Connect(); err != nil {
		return fmt.Errorf("failed to refresh cloak client: connect failed: %w", err)
	}
	return nil
}

func (c *CkClient) HealthCheck() error {
	return nil
}
