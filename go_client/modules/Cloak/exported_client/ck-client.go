//go:build go1.11
// +build go1.11

package exported_client

import (
	"errors"
	"encoding/binary"
	"fmt"
	"github.com/cbeuw/Cloak/internal/client"
	"github.com/cbeuw/Cloak/internal/common"
	mux "github.com/cbeuw/Cloak/internal/multiplex"
	"github.com/sirupsen/logrus"
	log "go_client/logger"
	"net"
	"strings"
	"sync"
	"time"
)

var errListenerClosed = errors.New("listener closed")

// closeQuiescingListener wraps a net.Listener so that once it is closed intentionally,
type closeQuiescingListener struct {
	net.Listener
	mu     sync.Mutex
	closed bool
}

func (l *closeQuiescingListener) Close() error {
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
	return l.Listener.Close()
}

func (l *closeQuiescingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		return c, nil
	}

	l.mu.Lock()
	closed := l.closed
	l.mu.Unlock()

	if closed && isClosedListenerErr(err) {
		// Terminate RouteTCP goroutine without hitting its "Accept error -> log.Fatal -> continue" path.
		panic(errListenerClosed)
	}
	return nil, err
}

func isClosedListenerErr(err error) bool {
	if errors.Is(err, net.ErrClosed) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "use of closed network connection")
}

type CkClient struct {
	mu        sync.Mutex
	connected bool
	config    client.RawConfig
	session   *mux.Session
	listener  net.Listener
	udpConn   *net.UDPConn
	routeDone chan struct{}
}

type Config client.RawConfig

func NewCkClient(config Config) *CkClient {
	return &CkClient{config: client.RawConfig(config)}
}

func (c *CkClient) Connect() (returnErr error) {
	defer func() {
		if r := recover(); r != nil {
			log.Infof("ck-client Connect: recovered from panic: %v", r)
			returnErr = fmt.Errorf("panic in Connect: %v", r)
		}
	}()
	
	c.mu.Lock()
	defer c.mu.Unlock()

	c.connected = true
	log.Infof("ck-client connected")

	localConfig, remoteConfig, authInfo, err := c.config.ProcessRawConfig(common.RealWorldState)
	if err != nil {
		c.connected = false
		return err
	}

	var adminUID []byte
	if len(c.config.UID) != 0 {
		adminUID = c.config.UID
		log.Infof("ck-client: adminUID set to %s", adminUID)
	}

	var seshMaker func() *mux.Session

	d := &net.Dialer{Control: protector, KeepAlive: remoteConfig.KeepAlive}

	if adminUID != nil {
		log.Infof("API base is %v", localConfig.LocalAddr)
		authInfo.UID = adminUID
		authInfo.SessionId = 0
		remoteConfig.NumConn = 1

		log.Infof("Before seshMaker")
		seshMaker = func() *mux.Session {
			log.Infof("In seshMaker")
			c.session = client.MakeSession(remoteConfig, authInfo, d)
			return c.session
		}
	} else {
		var network string
		if authInfo.Unordered {
			network = "UDP"
		} else {
			network = "TCP"
		}
		log.Infof("ck-client: Listening on %v %v for %v client", network, localConfig.LocalAddr, authInfo.ProxyMethod)
		seshMaker = func() *mux.Session {
			authInfo := authInfo // copy the struct because we are overwriting SessionId

			randByte := make([]byte, 1)
			common.RandRead(authInfo.WorldState.Rand, randByte)
			authInfo.MockDomain = localConfig.MockDomainList[int(randByte[0])%len(localConfig.MockDomainList)]

			// sessionID is usergenerated. There shouldn't be a security concern because the scope of
			// sessionID is limited to its UID.
			quad := make([]byte, 4)
			common.RandRead(authInfo.WorldState.Rand, quad)
			authInfo.SessionId = binary.BigEndian.Uint32(quad)
			c.session = client.MakeSession(remoteConfig, authInfo, d)
			return c.session
		}
	}

	done := make(chan struct{})
	c.routeDone = done

	go func() {
		// Signal completion so Disconnect() can wait for the routing goroutine to exit.
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				// Expected on normal shutdown (listener closed) — don't spam logs.
				if v, ok := r.(error); ok && errors.Is(v, errListenerClosed) {
					return
				}
				log.Infof("ck-client: recovered from panic from: %v", r)
			}
		}()
		if authInfo.Unordered {
			udpAddr, _ := net.ResolveUDPAddr("udp", localConfig.LocalAddr)
			conn, err := net.ListenUDP("udp", udpAddr)
			if err != nil {
				log.Infof("ck-client: goroutines: err %v\n", err)
				return
			}

			c.udpConn = conn

			log.Infof("ck-client: start listening on UDP %v for %v client", localConfig.LocalAddr, authInfo.ProxyMethod)
			client.RouteUDP(func() (*net.UDPConn, error) { return conn, nil }, localConfig.Timeout, remoteConfig.Singleplex, seshMaker)
			log.Infof("ck-client: stop listening on UDP %v for %v client", localConfig.LocalAddr, authInfo.ProxyMethod)
		} else {
			baseListener, err := net.Listen("tcp", localConfig.LocalAddr)
			if err != nil {
				log.Infof("ck-client: goroutines: err %v\n", err)
				return
			}

			l := &closeQuiescingListener{Listener: baseListener}
			c.listener = l

			log.Infof("ck-client: start listening on TCP %v for %v client", localConfig.LocalAddr, authInfo.ProxyMethod)
			client.RouteTCP(l, localConfig.Timeout, remoteConfig.Singleplex, seshMaker)
			log.Infof("ck-client: stop listening on TCP %v for %v client", localConfig.LocalAddr, authInfo.ProxyMethod)
		}
	}()

	return nil
}

func (c *CkClient) Disconnect() error {
	prevExit := logrus.StandardLogger().ExitFunc
	logrus.StandardLogger().ExitFunc = func(int) {}
	defer func() { logrus.StandardLogger().ExitFunc = prevExit }()

	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		log.Infof("ck-client: already disconnected")
		return nil
	}

	log.Infof("ck-client: initiating disconnect...")
	c.connected = false

	if c.listener != nil {
		addr := c.listener.Addr().String()
		if err := c.listener.Close(); err != nil {
			log.Infof("ck-client: error closing TCP listener %v: %v", addr, err)
		} else {
			log.Infof("ck-client: TCP listener %v closed", addr)
		}
		c.listener = nil
	}

	if c.udpConn != nil {
		addr := c.udpConn.LocalAddr().String()
		if err := c.udpConn.Close(); err != nil {
			log.Infof("ck-client: error closing UDP conn %v: %v", addr, err)
		} else {
			log.Infof("ck-client: UDP listener %v closed", addr)
		}
		c.udpConn = nil
	}

	if c.session != nil {
		log.Infof("ck-client: closing session...")
		c.session.Close()
		c.session = nil
		log.Infof("ck-client: session closed")
	}

	// Best-effort: wait for routing goroutine to exit so stop is deterministic.
	if c.routeDone != nil {
		select {
		case <-c.routeDone:
			// ok
		case <-time.After(2 * time.Second):
			log.Infof("ck-client: routing goroutine did not exit within timeout")
		}
		c.routeDone = nil
	}

	log.Infof("ck-client: fully disconnected")
	return nil
}

func (c *CkClient) Refresh() error {
	if err := c.Disconnect(); err != nil { // TODO: handle error with more detail
		return err
	}

	return c.Connect()
}
