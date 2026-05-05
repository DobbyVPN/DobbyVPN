package outline

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Jigsaw-Code/outline-sdk/transport"
	"github.com/Jigsaw-Code/outline-sdk/x/configurl"
	socks5 "github.com/things-go/go-socks5"

	"go_module/auth"
	"go_module/core/pkg"
	"go_module/log"
)

type OutlineDevice struct {
	mu               sync.RWMutex
	listener         net.Listener
	// listenerAddr is the raw "host:port" of the SOCKS5 listener (e.g. "127.0.0.1:54321").
	// It is the address used for:
	//   - probing liveness in LocalProxyAlive() via a plain net.Dial
	//   - rebinding the listener after an unexpected stop
	//   - log messages inside serveLoop / markServeRunning
	listenerAddr string
	// proxyAuthAddr is the full "user:pass@host:port" string passed to tun2socks as the
	// upstream SOCKS5 proxy address. It MUST NOT be passed to net.Dial or net.Listen.
	proxyAuthAddr    string
	svrIP            net.IP
	streamDialer     transport.StreamDialer
	packetDialer     transport.PacketDialer
	useCloak         bool
	preferTCPDNS     bool
	disableNonDNSUDP bool
	closed           atomic.Bool
	startedAt        time.Time
	serveState       string
	serveErr         string
	serveGen         int
	ctx              context.Context
	socksUser        string
	socksPass        string
}

type DeviceOptions struct {
	PreferTCPDNSForWebSocket bool
	// DisableNonDNSUDP rejects non-DNS UDP dials immediately.
	// Use this when the transport is WebSocket without a dedicated UDP path:
	// Shadowsocks AEAD UDP over WebSocket fails AEAD decryption under concurrency,
	// causing QUIC retry storms. Refusing UDP makes iOS/apps fall back to TCP instantly.
	DisableNonDNSUDP bool
}

func NewOutlineDevice(transportConfig string) (*OutlineDevice, error) {
	return NewOutlineDeviceWithOptions(transportConfig, DeviceOptions{})
}

func NewOutlineDeviceWithOptions(transportConfig string, options DeviceOptions) (*OutlineDevice, error) {
	ip, err := ResolveServerIPFromConfig(transportConfig)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()

	// Use custom providers with protected base dialers to ensure all upstream
	// connections (SS, WebSocket, etc.) correctly bypass the tunnel on iOS 26.
	providers := configurl.NewProviderContainer()
	providers.StreamDialers.BaseInstance = pkg.NewProtectedStreamDialer(transportConfig)
	providers.PacketDialers.BaseInstance = pkg.NewProtectedPacketDialer(transportConfig)
	configurl.RegisterDefaultProviders(providers)

	sd, err := providers.NewStreamDialer(ctx, transportConfig)
	if err != nil {
		return nil, err
	}

	pd, err := providers.NewPacketDialer(ctx, transportConfig)
	if err != nil {
		return nil, err
	}

	hasUDPPath := strings.Contains(transportConfig, "udp_path=")
	hasTCPPath := strings.Contains(transportConfig, "tcp_path=")
	isWebSocket := strings.Contains(transportConfig, "ws:")
	preferTCPDNS := options.PreferTCPDNSForWebSocket && isWebSocket
	log.Infof(
		"outline client: transport summary len=%d websocket=%v tcpPath=%v udpPath=%v preferTCPDNS=%v streamDialer=%T packetDialer=%T",
		len(transportConfig),
		isWebSocket,
		hasTCPPath,
		hasUDPPath,
		preferTCPDNS,
		sd,
		pd,
	)

	useCloak := ip.IsLoopback()

	log.Infof("outline client: cloak mode = %v", useCloak)

	socksUser := auth.GenerateRandomAuth()
	socksPass := auth.GenerateRandomAuth()

	od := &OutlineDevice{
		svrIP:            ip,
		streamDialer:     sd,
		packetDialer:     pd,
		useCloak:         useCloak,
		preferTCPDNS:     preferTCPDNS,
		disableNonDNSUDP: options.DisableNonDNSUDP,
		startedAt:        time.Now(),
		serveState:       "created",
		ctx:              ctx,
		socksUser:        socksUser,
		socksPass:        socksPass,
	}

	return od, nil
}

func (d *OutlineDevice) Open(routingTableID int, uplinkIface string) error {
	server := socks5.NewServer(
		socks5.WithDial(d.handleDial),
		socks5.WithCredential(socks5.StaticCredentials{
			d.socksUser: d.socksPass,
		}),
		socks5.WithLogger(socksLogger{}),
	)

	lc := net.ListenConfig{}

	listener, err := lc.Listen(d.ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}

	d.listener = listener
	d.listenerAddr = listener.Addr().String()
	d.proxyAuthAddr = fmt.Sprintf("%s:%s@%s", d.socksUser, d.socksPass, d.listenerAddr)
	log.Infof("SOCKS5 listener started: listenerAddr=%s", d.listenerAddr)

	go d.serveLoop(server)

	return nil
}

type socksLogger struct{}

func (socksLogger) Errorf(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	if strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "client want to used addr") {
		return
	}
	log.Infof("[SOCKS5 internal] %s", msg)
}

func (d *OutlineDevice) serveLoop(server *socks5.Server) {
	for {
		if d.closed.Load() {
			d.markServeStopped("closed")
			return
		}

		d.mu.RLock()
		listener := d.listener
		laddr := d.listenerAddr // raw host:port, safe for logging and rebind
		d.mu.RUnlock()

		if listener == nil {
			d.markServeStopped("listener=nil")
			return
		}

		d.markServeRunning()
		err := d.serveOnce(server, listener)
		if d.closed.Load() {
			d.markServeStopped("closed")
			log.Infof("SOCKS5 stopped on %s: closed", laddr)
			return
		}

		errText := "nil"
		if err != nil {
			errText = err.Error()
		}
		d.markServeStopped(errText)
		log.Infof("SOCKS5 stopped unexpectedly on %s: %s", laddr, errText)

		// Rebind to the same host:port (listenerAddr), not the auth string.
		for !d.closed.Load() {
			time.Sleep(250 * time.Millisecond)
			if err := d.rebindListener(laddr); err != nil {
				log.Infof("SOCKS5 rebind failed on %s: %v", laddr, err)
				time.Sleep(time.Second)
				continue
			}
			log.Infof("SOCKS5 rebound on %s", laddr)
			break
		}
	}
}

func (d *OutlineDevice) serveOnce(server *socks5.Server, listener net.Listener) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()

	log.Infof("SOCKS5 started on %s", listener.Addr().String())
	return server.Serve(listener)
}

// rebindListener re-creates the SOCKS5 TCP listener on the same host:port.
// addr must be the raw "host:port" (listenerAddr), NOT the auth string.
func (d *OutlineDevice) rebindListener(addr string) error {
	if d.closed.Load() {
		return net.ErrClosed
	}

	lc := net.ListenConfig{}
	listener, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return err
	}

	newAddr := listener.Addr().String()
	d.mu.Lock()
	d.listener = listener
	d.listenerAddr = newAddr
	// proxyAuthAddr credentials remain the same; only the port may shift if the
	// OS assigned a different port — update the host:port portion accordingly.
	d.proxyAuthAddr = fmt.Sprintf("%s:%s@%s", d.socksUser, d.socksPass, newAddr)
	d.mu.Unlock()
	return nil
}

func (d *OutlineDevice) markServeRunning() {
	d.mu.Lock()
	d.serveGen++
	d.serveState = "running"
	d.serveErr = ""
	gen := d.serveGen
	laddr := d.listenerAddr
	d.mu.Unlock()
	log.Infof("SOCKS5 serve generation %d running on %s", gen, laddr)
}

func (d *OutlineDevice) markServeStopped(reason string) {
	d.mu.Lock()
	d.serveState = "stopped"
	d.serveErr = reason
	d.mu.Unlock()
}

func (d *OutlineDevice) handleDial(ctx context.Context, network, addr string) (net.Conn, error) {
	log.Infof("[SOCKS5] dial %s %s", network, addr)
	start := time.Now()

	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)

	switch network {

	case "tcp":
		conn, err := d.streamDialer.DialStream(ctx, addr)
		elapsed := time.Since(start).Milliseconds()
		if err != nil {
			log.Infof("[SOCKS5 TCP ERROR] %s in %dms: %v", addr, elapsed, err)
			return nil, err
		}
		log.Infof("[SOCKS5 TCP OK] %s in %dms", addr, elapsed)
		log.Infof("[DEBUG][SOCKS5 TCP] %s local=%s remote=%s", addr, conn.LocalAddr(), conn.RemoteAddr())
		return conn, nil

	case "udp":

		// Force DNS-over-TCP fallback when UDP is known to be unreliable for this transport.
		if port == 53 && (d.useCloak || d.preferTCPDNS) {
			log.Infof("[SOCKS5 DNS] returning truncated DNS (useCloak=%v preferTCPDNS=%v)", d.useCloak, d.preferTCPDNS)
			return newTruncatedDNSConn(host, port), nil
		}

		// When using WebSocket transport without a dedicated UDP path, Shadowsocks AEAD UDP
		// frames are multiplexed over the TCP WebSocket stream. Under concurrency this produces
		// chacha20poly1305 AEAD failures on every response, causing QUIC retry storms.
		// Reject non-DNS UDP immediately so the OS falls back to TCP without burning ~1s per attempt.
		if d.disableNonDNSUDP && port != 53 {
			log.Infof("[SOCKS5 UDP] rejected (disableNonDNSUDP) addr=%s", addr)
			return nil, fmt.Errorf("non-DNS UDP disabled for this transport")
		}

		conn, err := d.packetDialer.DialPacket(ctx, addr)
		elapsed := time.Since(start).Milliseconds()
		if err != nil {
			log.Infof("[SOCKS5 UDP ERROR] %s in %dms: %v", addr, elapsed, err)
			return nil, err
		}
		log.Infof("[SOCKS5 UDP OK] %s in %dms", addr, elapsed)
		log.Infof("[DEBUG][SOCKS5 UDP] %s local=%s", addr, conn.LocalAddr())
		return conn, nil
	}

	return nil, fmt.Errorf("unsupported network %s", network)
}

type truncatedDNSConn struct {
	responses chan []byte
	done      chan struct{}
	closeOnce sync.Once
	remote    net.Addr
}

func newTruncatedDNSConn(host string, port int) net.Conn {
	return &truncatedDNSConn{
		responses: make(chan []byte, 16),
		done:      make(chan struct{}),
		remote:    &net.UDPAddr{IP: net.ParseIP(host), Port: port},
	}
}

func (c *truncatedDNSConn) Read(b []byte) (int, error) {
	select {
	case resp := <-c.responses:
		n := copy(b, resp)
		return n, nil
	case <-c.done:
		return 0, net.ErrClosed
	}
}

func (c *truncatedDNSConn) Write(b []byte) (int, error) {
	select {
	case <-c.done:
		return 0, net.ErrClosed
	default:
	}

	resp, err := truncatedDNSResponse(b)
	if err != nil {
		log.Infof("[SOCKS5 DNS] invalid DNS packet for TCP fallback: %v", err)
		return len(b), nil
	}

	select {
	case c.responses <- resp:
	case <-c.done:
		return 0, net.ErrClosed
	default:
		// Keep SOCKS5 UDP associate from blocking forever if a resolver floods
		// DNS requests faster than the client-side UDP relay can read replies.
		select {
		case <-c.responses:
		default:
		}
		select {
		case c.responses <- resp:
		case <-c.done:
			return 0, net.ErrClosed
		default:
		}
	}

	return len(b), nil
}

func truncatedDNSResponse(req []byte) ([]byte, error) {
	if len(req) < 12 {
		return nil, errors.New("invalid dns packet")
	}

	resp := make([]byte, len(req))
	copy(resp, req)

	// response
	resp[2] |= 0x80

	// truncated
	resp[2] |= 0x02

	// Preserve the original question but return no records. The TC bit is the
	// signal that the resolver should retry the same question over TCP.
	resp[6] = 0
	resp[7] = 0
	resp[8] = 0
	resp[9] = 0
	resp[10] = 0
	resp[11] = 0

	return resp, nil
}

func (c *truncatedDNSConn) Close() error {
	c.closeOnce.Do(func() {
		close(c.done)
	})
	return nil
}

func (c *truncatedDNSConn) LocalAddr() net.Addr                { return &net.UDPAddr{IP: net.IPv4zero, Port: 0} }
func (c *truncatedDNSConn) RemoteAddr() net.Addr               { return c.remote }
func (c *truncatedDNSConn) SetDeadline(t time.Time) error      { return nil }
func (c *truncatedDNSConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *truncatedDNSConn) SetWriteDeadline(t time.Time) error { return nil }

func ResolveServerIPFromConfig(transportConfig string) (net.IP, error) {

	if transportConfig = strings.TrimSpace(transportConfig); transportConfig == "" {
		return nil, errors.New("config is required")
	}

	host := extractTLSSNIHost(transportConfig)
	if host != "" {
		log.Infof("outline client: detected WSS config, using TLS SNI host: %s", host)
	} else {
		var err error
		host, err = extractSSHost(transportConfig)
		if err != nil {
			return nil, err
		}
		log.Infof("outline client: using ss:// host: %s", host)
	}

	if host == "127.0.0.1" || host == "localhost" {
		log.Infof("outline client: localhost detected, skipping IP resolution")
		return net.ParseIP("127.0.0.1").To4(), nil
	}

	resolver := net.Resolver{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ipList, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("DNS lookup for %s timed out or failed: %w", host, err)
	}
	log.Infof("outline client: DNS returned %d addresses for %s", len(ipList), host)

	for _, ip := range ipList {
		if v4 := ip.IP.To4(); v4 != nil {
			log.Infof("outline client: resolved %s -> %s", host, v4.String())
			return v4, nil
		}
	}

	return nil, errors.New("IPv6 only Shadowsocks server is not supported yet")
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

func (d *OutlineDevice) GetServerIP() net.IP {
	return d.svrIP
}

// GetProxyAddr returns the full "user:pass@host:port" address for tun2socks.
// Do NOT use this for net.Dial or net.Listen — use listenerAddr for that.
func (d *OutlineDevice) GetProxyAddr() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.proxyAuthAddr
}

// LocalProxyAlive probes the SOCKS5 listener with a plain TCP dial to listenerAddr
// (NOT proxyAuthAddr — the auth string is not a valid dial target).
func (d *OutlineDevice) LocalProxyAlive(timeout time.Duration) (alive bool, status string) {
	d.mu.RLock()
	laddr := d.listenerAddr   // raw host:port for net.Dial
	authAddr := d.proxyAuthAddr // auth string for diagnostics only
	state := d.serveState
	serveErr := d.serveErr
	gen := d.serveGen
	closed := d.closed.Load()
	startedAt := d.startedAt
	d.mu.RUnlock()

	if laddr == "" {
		return false, fmt.Sprintf(
			"localProxyAlive=false listenerAddr= proxyAuthAddr=%s serveState=%s serveGen=%d closed=%v serveErr=%q uptimeMs=%d",
			authAddr,
			state,
			gen,
			closed,
			serveErr,
			time.Since(startedAt).Milliseconds(),
		)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Probe using the plain host:port — this is what net.Dial expects.
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", laddr)
	if err != nil {
		return false, fmt.Sprintf(
			"localProxyAlive=false listenerAddr=%s proxyAuthAddr=%s serveState=%s serveGen=%d closed=%v serveErr=%q probeErr=%q uptimeMs=%d",
			laddr,
			authAddr,
			state,
			gen,
			closed,
			serveErr,
			err.Error(),
			time.Since(startedAt).Milliseconds(),
		)
	}
	_ = conn.Close()

	return true, fmt.Sprintf(
		"localProxyAlive=true listenerAddr=%s proxyAuthAddr=%s serveState=%s serveGen=%d closed=%v serveErr=%q uptimeMs=%d",
		laddr,
		authAddr,
		state,
		gen,
		closed,
		serveErr,
		time.Since(startedAt).Milliseconds(),
	)
}

func (d *OutlineDevice) Status(timeout time.Duration) string {
	_, status := d.LocalProxyAlive(timeout)
	return status
}

func (d *OutlineDevice) Close() error {
	d.closed.Store(true)
	d.mu.RLock()
	listener := d.listener
	laddr := d.listenerAddr
	d.mu.RUnlock()

	if listener != nil {
		log.Infof("SOCKS5 close requested on %s", laddr)
		return listener.Close()
	}
	return nil
}
