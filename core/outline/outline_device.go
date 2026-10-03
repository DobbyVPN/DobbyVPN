package outline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	socks5 "github.com/things-go/go-socks5"
	"golang.getoutline.org/sdk/transport"

	"core/auth"
	"core/dnscache"
	"core/log"
	"core/tunnel/protected_dialer"
)

const (
	networkTCP = "tcp"
	nilString  = "<nil>"
)

type OutlineDevice struct {
	mu           sync.Mutex
	closeMu      sync.Mutex
	connections  map[*ownedConn]struct{}
	workers      sync.WaitGroup
	ctx          context.Context
	cancel       context.CancelFunc
	closing      bool
	listener     net.Listener
	proxyAddr    string
	svrIP        net.IP
	streamDialer transport.StreamDialer
	packetDialer transport.PacketDialer
	websocket    bool
	hasTCPPath   bool
	hasUDPPath   bool
}

func NewOutlineDevice(transportConfig string, dnsCache *dnscache.Cache) (*OutlineDevice, error) {
	if dnsCache == nil {
		return nil, errors.New("DNS cache is required")
	}
	ip, err := ResolveServerIPFromConfig(transportConfig, dnsCache)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	providers := protected_dialer.NewOutlineProviders(dnsCache)

	sd, err := providers.NewStreamDialer(ctx, transportConfig)
	if err != nil {
		log.Debugf(Category, "outline client: failed to create stream dialer websocket=%v tcpPath=%v err=%v", strings.Contains(transportConfig, "ws:"), strings.Contains(transportConfig, "tcp_path="), err)
		return nil, err
	}

	pd, err := providers.NewPacketDialer(ctx, transportConfig)
	if err != nil {
		log.Debugf(Category, "outline client: failed to create packet dialer websocket=%v udpPath=%v err=%v", strings.Contains(transportConfig, "ws:"), strings.Contains(transportConfig, "udp_path="), err)
		return nil, err
	}

	isWebSocket := strings.Contains(transportConfig, "ws:")
	hasTCPPath := strings.Contains(transportConfig, "tcp_path=")
	hasUDPPath := strings.Contains(transportConfig, "udp_path=")

	log.Debugf(Category,
		"outline client: transport summary len=%d serverIP=%s websocket=%v tcpPath=%v udpPath=%v streamDialer=%T packetDialer=%T",
		len(transportConfig),
		ip.String(),
		isWebSocket,
		hasTCPPath,
		hasUDPPath,
		sd,
		pd,
	)
	ownerContext, cancel := context.WithCancel(context.Background())
	od := &OutlineDevice{
		ctx: ownerContext, cancel: cancel, connections: make(map[*ownedConn]struct{}),
		svrIP:        ip,
		streamDialer: sd,
		packetDialer: pd,
		websocket:    isWebSocket,
		hasTCPPath:   hasTCPPath,
		hasUDPPath:   hasUDPPath,
	}

	username, password := auth.GenerateRandomAuth(), auth.GenerateRandomAuth()
	server := socks5.NewServer(
		socks5.WithGPool(od),
		socks5.WithCredential(socks5.StaticCredentials{username: password}),
		socks5.WithDial(od.handleDial),
		socks5.WithLogger(socksLogger{device: od}),
	)

	lc := net.ListenConfig{}

	listener, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		cancel()
		return nil, err
	}

	od.listener = listener
	od.proxyAddr = url.UserPassword(username, password).String() + "@" + listener.Addr().String()

	_ = od.Submit(func() {
		log.Debugf(Category, "SOCKS5 started on %s", od.proxyAddr)
		if err := server.Serve(ownedListener{Listener: listener, owner: od}); err != nil {
			if errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "use of closed network connection") {
				log.Debugf(Category, "SOCKS5 stopped on %s: closed", od.proxyAddr)
			} else {
				log.Errorf(Category, "SOCKS5 stopped unexpectedly on %s: %v", od.proxyAddr, err)
			}
		} else {
			log.Debugf(Category, "SOCKS5 stopped on %s: nil error", od.proxyAddr)
		}
	})

	return od, nil
}

type socksLogger struct {
	device *OutlineDevice
}

func (l socksLogger) Errorf(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	if strings.Contains(msg, "chacha20poly1305: message authentication failed") && l.device != nil {
		log.Errorf(Category,
			"[SOCKS5 internal][auth_error websocket=%v tcpPath=%v udpPath=%v packetDialer=%T] %s",
			l.device.websocket,
			l.device.hasTCPPath,
			l.device.hasUDPPath,
			l.device.packetDialer,
			msg,
		)
		return
	}
	log.Errorf(Category, "[SOCKS5 internal] %s", msg)
}

func (d *OutlineDevice) handleDial(ctx context.Context, network, addr string) (net.Conn, error) {
	ctx, cancel := d.dialContext(ctx)
	defer cancel()

	start := time.Now()
	serverIP := d.serverIPString()

	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	if host == "" || port == 0 {
		log.Warnf(Category, "[SOCKS5 DIAL WARN] network=%s addr=%s parsedHost=%s parsedPort=%d", network, addr, host, port)
	}

	switch network {

	case networkTCP:
		conn, err := d.streamDialer.DialStream(ctx, addr)
		if err != nil {
			log.Errorf(Category, "[SOCKS5 TCP ERROR] dst=%s server=%s elapsed=%s ctxErr=%v cause=%s err=%v", addr, serverIP, time.Since(start), ctx.Err(), contextCause(ctx), err)
			return nil, fmt.Errorf("StreamDialer failed for %s: %w", addr, err)
		}

		return d.track(conn)

	case "udp":
		// Some platform resolvers did not receive replies through otherwise
		// healthy Outline UDP transports in qualification, while their TCP retry
		// path was reliable. Keep the standards-compliant fallback scoped by OS.
		if forceTCPDNSForPlatform && port == 53 {
			log.Debugf(Category, "[SOCKS5 DNS] returning truncated DNS addr=%s", addr)

			return d.track(newTruncatedDNSConn())
		}

		conn, err := d.packetDialer.DialPacket(ctx, addr)
		if err != nil {
			log.Errorf(Category, "[SOCKS5 UDP ERROR] dst=%s server=%s elapsed=%s ctxErr=%v cause=%s err=%v", addr, serverIP, time.Since(start), ctx.Err(), contextCause(ctx), err)
			return nil, fmt.Errorf("PacketDialer failed for %s: %w", addr, err)
		}

		return d.track(conn)
	}

	err := fmt.Errorf("unsupported network %s", network)
	log.Errorf(Category, "[SOCKS5 ERROR] dst=%s server=%s elapsed=%s err=%v", addr, serverIP, time.Since(start), err)
	return nil, err
}

func (d *OutlineDevice) serverIPString() string {
	if d.svrIP == nil {
		return nilString
	}
	return d.svrIP.String()
}

func contextCause(ctx context.Context) string {
	if ctx == nil {
		return nilString
	}
	if cause := context.Cause(ctx); cause != nil {
		return cause.Error()
	}
	return nilString
}

type truncatedDNSConn struct {
	mu      sync.Mutex
	ready   *sync.Cond
	pending []byte
	readErr error
	closed  bool
}

func newTruncatedDNSConn() net.Conn {
	c := &truncatedDNSConn{}
	c.ready = sync.NewCond(&c.mu)
	return c
}

func (c *truncatedDNSConn) Read(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.pending) == 0 && c.readErr == nil && !c.closed {
		c.ready.Wait()
	}
	if c.readErr != nil {
		err := c.readErr
		c.readErr = nil
		return 0, err
	}
	if len(c.pending) == 0 && c.closed {
		return 0, io.EOF
	}
	n := copy(b, c.pending)
	c.pending = c.pending[n:]
	if len(c.pending) == 0 {
		c.ready.Broadcast()
	}
	return n, nil
}

func (c *truncatedDNSConn) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.pending) != 0 && !c.closed {
		c.ready.Wait()
	}
	if c.closed {
		return 0, net.ErrClosed
	}
	if len(b) < 12 {
		c.readErr = errors.New("invalid dns packet")
		c.ready.Broadcast()
		return len(b), nil
	}
	c.pending = append(c.pending[:0], b...)
	// Return one synthetic DNS response for this request. The SOCKS UDP relay
	// reads until EOF/error, so replaying the same response on every Read would
	// create an unbounded local packet-feedback loop.
	c.pending[2] |= 0x80 // response
	c.pending[2] |= 0x02 // truncated: retry using TCP
	c.pending[6] = 0
	c.pending[7] = 0
	c.ready.Broadcast()
	return len(b), nil
}

func (c *truncatedDNSConn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.pending = nil
	c.ready.Broadcast()
	c.mu.Unlock()
	return nil
}
func (c *truncatedDNSConn) LocalAddr() net.Addr                { return nil }
func (c *truncatedDNSConn) RemoteAddr() net.Addr               { return nil }
func (c *truncatedDNSConn) SetDeadline(t time.Time) error      { return nil }
func (c *truncatedDNSConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *truncatedDNSConn) SetWriteDeadline(t time.Time) error { return nil }

func ResolveServerIPFromConfig(transportConfig string, dnsCache *dnscache.Cache) (net.IP, error) {
	if dnsCache == nil {
		return nil, errors.New("DNS cache is required")
	}

	if transportConfig = strings.TrimSpace(transportConfig); transportConfig == "" {
		return nil, errors.New("config is required")
	}

	host := extractTLSSNIHost(transportConfig)
	if host != "" {
		log.Debugf(Category, "outline client: detected WSS config, using TLS SNI host: %s", host)
	} else {
		var err error
		host, err = extractSSHost(transportConfig)
		if err != nil {
			return nil, err
		}
		log.Debugf(Category, "outline client: using ss:// host: %s", host)
	}

	if host == "127.0.0.1" || host == "localhost" {
		log.Debugf(Category, "outline client: localhost detected, skipping IP resolution")
		return net.ParseIP("127.0.0.1").To4(), nil
	}

	ip, err := dnsCache.ResolvePreflightIPv4(context.Background(), host, dnscache.ServerResolveTimeout, "outline")
	if err != nil {
		return nil, err
	}
	log.Debugf(Category, "outline client: resolved %s -> %s", host, ip.String())
	return ip, nil
}

func extractTLSSNIHost(transportConfig string) string {

	parts := strings.Split(transportConfig, "|")

	for _, part := range parts {

		part = strings.TrimSpace(part)

		if strings.HasPrefix(part, "tls:") {

			params := strings.TrimPrefix(part, "tls:")

			for _, param := range strings.Split(params, "&") {

				if strings.HasPrefix(param, "sni=") {
					return strings.TrimPrefix(param, "sni=")
				}
			}
		}
	}

	return ""
}

func extractSSHost(transportConfig string) (string, error) {

	parts := strings.Split(transportConfig, "|")

	for _, part := range parts {

		part = strings.TrimSpace(part)

		if strings.HasPrefix(part, "ss://") {

			u, err := url.Parse(part)
			if err != nil {
				return "", err
			}

			return u.Hostname(), nil
		}
	}

	return "", errors.New("ss:// not found")
}

// Open implements protocol.ProtocolDevice. The SOCKS5 server is already started
// by NewOutlineDevice, so this is a no-op.
func (d *OutlineDevice) Open(routingTableID int, uplinkIface string) error {
	if d == nil {
		return errors.New("outline device is not initialized")
	}
	return nil
}

func (d *OutlineDevice) GetServerIP() net.IP {
	if d == nil {
		return nil
	}
	return d.svrIP
}

func (d *OutlineDevice) GetProxyAddr() string {
	if d == nil {
		return ""
	}
	return d.proxyAddr
}
