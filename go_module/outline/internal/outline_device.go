package internal

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

	"go_module/log"
)

type OutlineDevice struct {
	mu           sync.RWMutex
	listener     net.Listener
	proxyAddr    string
	svrIP        net.IP
	streamDialer transport.StreamDialer
	packetDialer transport.PacketDialer
	useCloak     bool
	preferTCPDNS bool
	closed       atomic.Bool
	startedAt    time.Time
	serveState   string
	serveErr     string
	serveGen     int
}

type DeviceOptions struct {
	PreferTCPDNSForWebSocket bool
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
	providers.StreamDialers.BaseInstance = NewProtectedStreamDialer(transportConfig)
	providers.PacketDialers.BaseInstance = NewProtectedPacketDialer(transportConfig)
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

	od := &OutlineDevice{
		svrIP:        ip,
		streamDialer: sd,
		packetDialer: pd,
		useCloak:     useCloak,
		preferTCPDNS: preferTCPDNS,
		startedAt:    time.Now(),
		serveState:   "created",
	}

	server := socks5.NewServer(
		socks5.WithDial(od.handleDial),
		socks5.WithLogger(socksLogger{}),
	)

	lc := net.ListenConfig{}

	listener, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	od.listener = listener
	od.proxyAddr = listener.Addr().String()

	go od.serveLoop(server)

	return od, nil
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
		addr := d.proxyAddr
		d.mu.RUnlock()

		if listener == nil {
			d.markServeStopped("listener=nil")
			return
		}

		d.markServeRunning()
		err := d.serveOnce(server, listener)
		if d.closed.Load() {
			d.markServeStopped("closed")
			log.Infof("SOCKS5 stopped on %s: closed", addr)
			return
		}

		errText := "nil"
		if err != nil {
			errText = err.Error()
		}
		d.markServeStopped(errText)
		log.Infof("SOCKS5 stopped unexpectedly on %s: %s", addr, errText)

		for !d.closed.Load() {
			time.Sleep(250 * time.Millisecond)
			if err := d.rebindListener(addr); err != nil {
				log.Infof("SOCKS5 rebind failed on %s: %v", addr, err)
				time.Sleep(time.Second)
				continue
			}
			log.Infof("SOCKS5 rebound on %s", addr)
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

func (d *OutlineDevice) rebindListener(addr string) error {
	if d.closed.Load() {
		return net.ErrClosed
	}

	lc := net.ListenConfig{}
	listener, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return err
	}

	d.mu.Lock()
	d.listener = listener
	d.proxyAddr = listener.Addr().String()
	d.mu.Unlock()
	return nil
}

func (d *OutlineDevice) markServeRunning() {
	d.mu.Lock()
	d.serveGen++
	d.serveState = "running"
	d.serveErr = ""
	gen := d.serveGen
	addr := d.proxyAddr
	d.mu.Unlock()
	log.Infof("SOCKS5 serve generation %d running on %s", gen, addr)
}

func (d *OutlineDevice) markServeStopped(reason string) {
	d.mu.Lock()
	d.serveState = "stopped"
	d.serveErr = reason
	d.mu.Unlock()
}

func (d *OutlineDevice) handleDial(ctx context.Context, network, addr string) (net.Conn, error) {

	// iOS 26 research: Log detailed connection attempt to track which path is used
	log.Infof("[SOCKS5] dial %s %s (research: tracking connection path for iOS 26)", network, addr)
	start := time.Now()

	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)

	switch network {

	case "tcp":
		conn, err := d.streamDialer.DialStream(ctx, addr)
		elapsed := time.Since(start).Milliseconds()
		if err != nil {
			log.Infof("[SOCKS5 TCP ERROR] %s in %dms: %v", addr, elapsed, err)
			return nil, fmt.Errorf("StreamDialer failed for %s: %w", addr, err)
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

		conn, err := d.packetDialer.DialPacket(ctx, addr)
		elapsed := time.Since(start).Milliseconds()
		if err != nil {
			log.Infof("[SOCKS5 UDP ERROR] %s in %dms: %v", addr, elapsed, err)
			return nil, fmt.Errorf("PacketDialer failed for %s: %w", addr, err)
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

func (d *OutlineDevice) GetProxyAddr() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.proxyAddr
}

func (d *OutlineDevice) LocalProxyAlive(timeout time.Duration) (bool, string) {
	d.mu.RLock()
	addr := d.proxyAddr
	state := d.serveState
	serveErr := d.serveErr
	gen := d.serveGen
	closed := d.closed.Load()
	startedAt := d.startedAt
	d.mu.RUnlock()

	if addr == "" {
		return false, fmt.Sprintf(
			"localProxyAlive=false proxyAddr= serveState=%s serveGen=%d closed=%v serveErr=%q uptimeMs=%d",
			state,
			gen,
			closed,
			serveErr,
			time.Since(startedAt).Milliseconds(),
		)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return false, fmt.Sprintf(
			"localProxyAlive=false proxyAddr=%s serveState=%s serveGen=%d closed=%v serveErr=%q probeErr=%q uptimeMs=%d",
			addr,
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
		"localProxyAlive=true proxyAddr=%s serveState=%s serveGen=%d closed=%v serveErr=%q uptimeMs=%d",
		addr,
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
	addr := d.proxyAddr
	d.mu.RUnlock()

	if listener != nil {
		log.Infof("SOCKS5 close requested on %s", addr)
		return listener.Close()
	}
	return nil
}
