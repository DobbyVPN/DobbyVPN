package internal

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"runtime/debug"
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
	listener     net.Listener
	proxyAddr    string
	svrIP        net.IP
	streamDialer transport.StreamDialer
	packetDialer transport.PacketDialer
	useCloak     bool
	websocket    bool
	hasTCPPath   bool
	hasUDPPath   bool
	startedAt    time.Time
	closed       chan struct{}
	closeOnce    sync.Once

	tcpDialAttempt  atomic.Uint64
	tcpDialOK       atomic.Uint64
	tcpDialErr      atomic.Uint64
	tcpDialInFlight atomic.Int64
	tcpDialPeak     atomic.Int64
	udpDialAttempt  atomic.Uint64
	udpDialOK       atomic.Uint64
	udpDialErr      atomic.Uint64
	udpDialInFlight atomic.Int64
	udpDialPeak     atomic.Int64
	udpDNSTruncated atomic.Uint64
	unsupportedDial atomic.Uint64
	tcpConnClosed   atomic.Uint64
	tcpConnReadErr  atomic.Uint64
	tcpConnWriteErr atomic.Uint64
	tcpBytesRead    atomic.Uint64
	tcpBytesWritten atomic.Uint64
	udpConnClosed   atomic.Uint64
	udpConnReadErr  atomic.Uint64
	udpConnWriteErr atomic.Uint64
	udpBytesRead    atomic.Uint64
	udpBytesWritten atomic.Uint64
	socksServeExit  atomic.Uint64
	statsLoopExit   atomic.Uint64
}

var (
	socksInternalAuthErr       atomic.Uint64
	socksInternalResetErr      atomic.Uint64
	socksInternalBrokenPipeErr atomic.Uint64
	socksInternalEOF           atomic.Uint64
	socksInternalClosed        atomic.Uint64
	socksInternalAddrWarning   atomic.Uint64
	socksInternalOther         atomic.Uint64
)

func resetSocksInternalStats() {
	socksInternalAuthErr.Store(0)
	socksInternalResetErr.Store(0)
	socksInternalBrokenPipeErr.Store(0)
	socksInternalEOF.Store(0)
	socksInternalClosed.Store(0)
	socksInternalAddrWarning.Store(0)
	socksInternalOther.Store(0)
}

func NewOutlineDevice(transportConfig string) (*OutlineDevice, error) {
	resetSocksInternalStats()

	ip, err := ResolveServerIPFromConfig(transportConfig)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	providers := configurl.NewDefaultProviders()

	sd, err := providers.NewStreamDialer(ctx, transportConfig)
	if err != nil {
		log.Infof("outline client: failed to create stream dialer websocket=%v tcpPath=%v err=%v", strings.Contains(transportConfig, "ws:"), strings.Contains(transportConfig, "tcp_path="), err)
		return nil, err
	}

	pd, err := providers.NewPacketDialer(ctx, transportConfig)
	if err != nil {
		log.Infof("outline client: failed to create packet dialer websocket=%v udpPath=%v err=%v", strings.Contains(transportConfig, "ws:"), strings.Contains(transportConfig, "udp_path="), err)
		return nil, err
	}

	useCloak := ip.IsLoopback()
	isWebSocket := strings.Contains(transportConfig, "ws:")
	hasTCPPath := strings.Contains(transportConfig, "tcp_path=")
	hasUDPPath := strings.Contains(transportConfig, "udp_path=")

	log.Infof(
		"outline client: transport summary len=%d serverIP=%s websocket=%v tcpPath=%v udpPath=%v streamDialer=%T packetDialer=%T",
		len(transportConfig),
		ip.String(),
		isWebSocket,
		hasTCPPath,
		hasUDPPath,
		sd,
		pd,
	)
	log.Infof("outline client: cloak mode = %v", useCloak)

	od := &OutlineDevice{
		svrIP:        ip,
		streamDialer: sd,
		packetDialer: pd,
		useCloak:     useCloak,
		websocket:    isWebSocket,
		hasTCPPath:   hasTCPPath,
		hasUDPPath:   hasUDPPath,
		startedAt:    time.Now(),
		closed:       make(chan struct{}),
	}

	server := socks5.NewServer(
		socks5.WithDial(od.handleDial),
		socks5.WithLogger(socksLogger{device: od}),
	)

	lc := net.ListenConfig{}

	listener, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	od.listener = listener
	od.proxyAddr = listener.Addr().String()

	od.runGuarded("socks5-serve", func() {
		log.Infof("SOCKS5 started on %s", od.proxyAddr)
		if err := server.Serve(listener); err != nil {
			od.socksServeExit.Add(1)
			if errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "use of closed network connection") {
				log.Infof("SOCKS5 stopped on %s: closed stats={%s}", od.proxyAddr, od.dialStats())
			} else {
				log.Infof("SOCKS5 stopped unexpectedly on %s: %v stats={%s}", od.proxyAddr, err, od.dialStats())
			}
		} else {
			od.socksServeExit.Add(1)
			log.Infof("SOCKS5 stopped on %s: nil error stats={%s}", od.proxyAddr, od.dialStats())
		}
	})
	od.runGuarded("socks5-stats-loop", od.logStatsLoop)

	return od, nil
}

type socksLogger struct {
	device *OutlineDevice
}

func (l socksLogger) Errorf(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	if strings.Contains(msg, "EOF") {
		count := socksInternalEOF.Add(1)
		if l.device != nil && (count == 1 || count%50 == 0) {
			log.Infof("[SOCKS5 internal][eof count=%d] %s stats={%s}", count, msg, l.device.dialStats())
		}
		return
	}
	if strings.Contains(msg, "use of closed network connection") {
		count := socksInternalClosed.Add(1)
		if l.device != nil && (count == 1 || count%50 == 0) {
			log.Infof("[SOCKS5 internal][closed count=%d] %s stats={%s}", count, msg, l.device.dialStats())
		}
		return
	}
	if strings.Contains(msg, "client want to used addr") {
		count := socksInternalAddrWarning.Add(1)
		if l.device != nil && (count == 1 || count%50 == 0) {
			log.Infof("[SOCKS5 internal][addr_warning count=%d] %s stats={%s}", count, msg, l.device.dialStats())
		}
		return
	}
	if strings.Contains(msg, "chacha20poly1305: message authentication failed") {
		count := socksInternalAuthErr.Add(1)
		if l.device != nil {
			log.Infof(
				"[SOCKS5 internal][auth_error count=%d websocket=%v tcpPath=%v udpPath=%v packetDialer=%T] %s",
				count,
				l.device.websocket,
				l.device.hasTCPPath,
				l.device.hasUDPPath,
				l.device.packetDialer,
				msg,
			)
			if count == 1 || count%25 == 0 {
				log.Infof(
					"[SOCKS5 UDP DIAG] auth errors mean UDP responses could not be decrypted; websocket=%v udpPath=%v packetDialer=%T",
					l.device.websocket,
					l.device.hasUDPPath,
					l.device.packetDialer,
				)
				log.Infof("[ErrorStats] %s", l.device.errorRateStats())
			}
		} else {
			log.Infof("[SOCKS5 internal][auth_error count=%d] %s", count, msg)
		}
		return
	}
	if strings.Contains(msg, "connection reset by peer") {
		count := socksInternalResetErr.Add(1)
		log.Infof("[SOCKS5 internal][reset count=%d] %s", count, msg)
		if (count == 1 || count%10 == 0) && l.device != nil {
			log.Infof("[ErrorStats] %s", l.device.errorRateStats())
		}
		return
	}
	if strings.Contains(msg, "broken pipe") {
		count := socksInternalBrokenPipeErr.Add(1)
		log.Infof("[SOCKS5 internal][broken_pipe count=%d] %s", count, msg)
		if (count == 1 || count%10 == 0) && l.device != nil {
			log.Infof("[ErrorStats] %s", l.device.errorRateStats())
		}
		return
	}
	count := socksInternalOther.Add(1)
	log.Infof("[SOCKS5 internal][other count=%d] %s", count, msg)
	if (count == 1 || count%10 == 0) && l.device != nil {
		log.Infof("[ErrorStats] %s", l.device.errorRateStats())
	}
}

func (d *OutlineDevice) handleDial(ctx context.Context, network, addr string) (net.Conn, error) {

	start := time.Now()
	serverIP := d.serverIPString()

	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	if host == "" || port == 0 {
		log.Infof("[SOCKS5 DIAL WARN] network=%s addr=%s parsedHost=%s parsedPort=%d", network, addr, host, port)
	}

	switch network {

	case "tcp":
		attempt := d.tcpDialAttempt.Add(1)
		inFlight := d.tcpDialInFlight.Add(1)
		updatePeakInt64(&d.tcpDialPeak, inFlight)
		log.Infof("[SOCKS5 TCP BEGIN] attempt=%d dst=%s server=%s inFlight=%d stats={%s}", attempt, addr, serverIP, inFlight, d.dialStats())
		defer d.tcpDialInFlight.Add(-1)

		conn, err := d.streamDialer.DialStream(ctx, addr)
		if err != nil {
			d.tcpDialErr.Add(1)
			log.Infof("[SOCKS5 TCP ERROR] attempt=%d dst=%s server=%s elapsed=%s ctxErr=%v cause=%s stats={%s} errClass=%s err=%v", attempt, addr, serverIP, time.Since(start), ctx.Err(), contextCause(ctx), d.dialStats(), classifyOutlineIOErr(err), err)
			return nil, err
		}

		d.tcpDialOK.Add(1)
		log.Infof("[SOCKS5 TCP OK] attempt=%d dst=%s server=%s elapsed=%s local=%s remote=%s stats={%s}", attempt, addr, serverIP, time.Since(start), conn.LocalAddr(), conn.RemoteAddr(), d.dialStats())
		return d.wrapConn("tcp", attempt, addr, conn), nil

	case "udp":
		attempt := d.udpDialAttempt.Add(1)
		inFlight := d.udpDialInFlight.Add(1)
		updatePeakInt64(&d.udpDialPeak, inFlight)
		log.Infof("[SOCKS5 UDP BEGIN] attempt=%d dst=%s server=%s inFlight=%d stats={%s}", attempt, addr, serverIP, inFlight, d.dialStats())
		defer d.udpDialInFlight.Add(-1)

		// DNS fallback for Cloak
		if d.useCloak && port == 53 {
			d.udpDNSTruncated.Add(1)

			log.Infof("[SOCKS5 DNS] returning truncated DNS attempt=%d addr=%s cloak=%v stats={%s}", attempt, addr, d.useCloak, d.dialStats())

			return newTruncatedDNSConn(host, port), nil
		}

		conn, err := d.packetDialer.DialPacket(ctx, addr)
		if err != nil {
			d.udpDialErr.Add(1)
			log.Infof("[SOCKS5 UDP ERROR] attempt=%d dst=%s server=%s elapsed=%s ctxErr=%v cause=%s stats={%s} errClass=%s err=%v", attempt, addr, serverIP, time.Since(start), ctx.Err(), contextCause(ctx), d.dialStats(), classifyOutlineIOErr(err), err)
			return nil, err
		}

		d.udpDialOK.Add(1)
		log.Infof("[SOCKS5 UDP OK] attempt=%d dst=%s server=%s elapsed=%s local=%s stats={%s}", attempt, addr, serverIP, time.Since(start), conn.LocalAddr(), d.dialStats())
		return d.wrapConn("udp", attempt, addr, conn), nil
	}

	err := fmt.Errorf("unsupported network %s", network)
	d.unsupportedDial.Add(1)
	log.Infof("[SOCKS5 ERROR] dst=%s server=%s elapsed=%s err=%v", addr, serverIP, time.Since(start), err)
	return nil, err
}

func updatePeakInt64(peak *atomic.Int64, current int64) {
	for {
		old := peak.Load()
		if current <= old || peak.CompareAndSwap(old, current) {
			return
		}
	}
}

func (d *OutlineDevice) dialStats() string {
	return fmt.Sprintf(
		"uptime=%s tcp=%d/%d/%d/inflight=%d/peak=%d tcpIO=closed:%d/readErr:%d/writeErr:%d/readMB:%.2f/writeMB:%.2f udp=%d/%d/%d/inflight=%d/peak=%d udpIO=closed:%d/readErr:%d/writeErr:%d/readMB:%.2f/writeMB:%.2f udpDNSTrunc=%d unsupported=%d internalAuth=%d internalReset=%d internalBrokenPipe=%d internalEOF=%d internalClosed=%d internalAddrWarn=%d internalOther=%d goroutineExit=serve:%d/stats:%d",
		time.Since(d.startedAt).Truncate(time.Millisecond),
		d.tcpDialAttempt.Load(),
		d.tcpDialOK.Load(),
		d.tcpDialErr.Load(),
		d.tcpDialInFlight.Load(),
		d.tcpDialPeak.Load(),
		d.tcpConnClosed.Load(),
		d.tcpConnReadErr.Load(),
		d.tcpConnWriteErr.Load(),
		float64(d.tcpBytesRead.Load())/(1024*1024),
		float64(d.tcpBytesWritten.Load())/(1024*1024),
		d.udpDialAttempt.Load(),
		d.udpDialOK.Load(),
		d.udpDialErr.Load(),
		d.udpDialInFlight.Load(),
		d.udpDialPeak.Load(),
		d.udpConnClosed.Load(),
		d.udpConnReadErr.Load(),
		d.udpConnWriteErr.Load(),
		float64(d.udpBytesRead.Load())/(1024*1024),
		float64(d.udpBytesWritten.Load())/(1024*1024),
		d.udpDNSTruncated.Load(),
		d.unsupportedDial.Load(),
		socksInternalAuthErr.Load(),
		socksInternalResetErr.Load(),
		socksInternalBrokenPipeErr.Load(),
		socksInternalEOF.Load(),
		socksInternalClosed.Load(),
		socksInternalAddrWarning.Load(),
		socksInternalOther.Load(),
		d.socksServeExit.Load(),
		d.statsLoopExit.Load(),
	)
}

func (d *OutlineDevice) errorRateStats() string {
	tcpAttempt := d.tcpDialAttempt.Load()
	tcpFail := d.tcpDialErr.Load()
	udpAttempt := d.udpDialAttempt.Load()
	authErr := socksInternalAuthErr.Load()
	resetErr := socksInternalResetErr.Load()
	brokenPipeErr := socksInternalBrokenPipeErr.Load()
	otherErr := socksInternalOther.Load()

	pct := func(num, denom uint64) float64 {
		if denom == 0 {
			return 0
		}
		return float64(num) / float64(denom) * 100
	}

	return fmt.Sprintf(
		"udpAuth=%.1f%%(%d/%d) tcpFail=%.1f%%(%d/%d) tcpReset=%.1f%%(%d/%d) brokePipe=%d other=%d",
		pct(authErr, udpAttempt), authErr, udpAttempt,
		pct(tcpFail, tcpAttempt), tcpFail, tcpAttempt,
		pct(resetErr, tcpAttempt), resetErr, tcpAttempt,
		brokenPipeErr,
		otherErr,
	)
}

func (d *OutlineDevice) logStatsLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			log.Infof("[SOCKS5 STATS] proxy=%s server=%s stats={%s}", d.proxyAddr, d.serverIPString(), d.dialStats())
		case <-d.closed:
			d.statsLoopExit.Add(1)
			log.Infof("[SOCKS5 STATS] proxy=%s stopped stats={%s}", d.proxyAddr, d.dialStats())
			return
		}
	}
}

func (d *OutlineDevice) runGuarded(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Infof("[OUTLINE PANIC] goroutine=%s panic=%v stats={%s}\n%s", name, r, d.dialStats(), string(debug.Stack()))
			}
		}()
		fn()
	}()
}

func (d *OutlineDevice) serverIPString() string {
	if d.svrIP == nil {
		return "<nil>"
	}
	return d.svrIP.String()
}

func (d *OutlineDevice) wrapConn(network string, attempt uint64, dst string, conn net.Conn) net.Conn {
	return &outlineLoggedConn{
		Conn:      conn,
		device:    d,
		network:   network,
		attempt:   attempt,
		dst:       dst,
		server:    d.serverIPString(),
		startedAt: time.Now(),
	}
}

type outlineLoggedConn struct {
	net.Conn

	device    *OutlineDevice
	network   string
	attempt   uint64
	dst       string
	server    string
	startedAt time.Time

	readBytes    atomic.Uint64
	writtenBytes atomic.Uint64
	readErrOnce  sync.Once
	writeErrOnce sync.Once
	closeOnce    sync.Once
	lastErr      atomic.Value
}

func (c *outlineLoggedConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.readBytes.Add(uint64(n))
		if c.network == "tcp" {
			c.device.tcpBytesRead.Add(uint64(n))
		} else {
			c.device.udpBytesRead.Add(uint64(n))
		}
	}
	if err != nil && !errors.Is(err, net.ErrClosed) {
		c.recordIOErr("read", err)
	}
	return n, err
}

func (c *outlineLoggedConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	if n > 0 {
		c.writtenBytes.Add(uint64(n))
		if c.network == "tcp" {
			c.device.tcpBytesWritten.Add(uint64(n))
		} else {
			c.device.udpBytesWritten.Add(uint64(n))
		}
	}
	if err != nil && !errors.Is(err, net.ErrClosed) {
		c.recordIOErr("write", err)
	}
	return n, err
}

func (c *outlineLoggedConn) Close() error {
	err := c.Conn.Close()
	c.closeOnce.Do(func() {
		var closed uint64
		if c.network == "tcp" {
			closed = c.device.tcpConnClosed.Add(1)
		} else {
			closed = c.device.udpConnClosed.Add(1)
		}
		lastErr := "<nil>"
		if v := c.lastErr.Load(); v != nil {
			lastErr = v.(string)
		}
		readMB := float64(c.readBytes.Load()) / (1024 * 1024)
		writeMB := float64(c.writtenBytes.Load()) / (1024 * 1024)
		shouldLog := lastErr != "<nil>" || closed == 1 || closed%25 == 0 || readMB >= 1 || writeMB >= 1 || time.Since(c.startedAt) >= 5*time.Second
		if shouldLog {
			log.Infof(
				"[OUTLINE FLOW CLOSE] network=%s attempt=%d dst=%s server=%s lifetime=%s readMB=%.2f writeMB=%.2f local=%s remote=%s closeErr=%v lastIOErr=%s stats={%s}",
				c.network,
				c.attempt,
				c.dst,
				c.server,
				time.Since(c.startedAt).Truncate(time.Millisecond),
				readMB,
				writeMB,
				c.safeLocalAddr(),
				c.safeRemoteAddr(),
				err,
				lastErr,
				c.device.dialStats(),
			)
		}
	})
	return err
}

func (c *outlineLoggedConn) recordIOErr(op string, err error) {
	errText := fmt.Sprintf("%s:%s", classifyOutlineIOErr(err), err.Error())
	c.lastErr.Store(errText)
	if c.network == "tcp" {
		if op == "read" {
			c.device.tcpConnReadErr.Add(1)
			c.readErrOnce.Do(func() { c.logIOErr(op, errText) })
		} else {
			c.device.tcpConnWriteErr.Add(1)
			c.writeErrOnce.Do(func() { c.logIOErr(op, errText) })
		}
		return
	}
	if op == "read" {
		c.device.udpConnReadErr.Add(1)
		c.readErrOnce.Do(func() { c.logIOErr(op, errText) })
	} else {
		c.device.udpConnWriteErr.Add(1)
		c.writeErrOnce.Do(func() { c.logIOErr(op, errText) })
	}
}

func (c *outlineLoggedConn) logIOErr(op, errText string) {
	log.Infof(
		"[OUTLINE FLOW IO ERROR] network=%s op=%s attempt=%d dst=%s server=%s lifetime=%s readBytes=%d writeBytes=%d local=%s remote=%s err=%s stats={%s}",
		c.network,
		op,
		c.attempt,
		c.dst,
		c.server,
		time.Since(c.startedAt).Truncate(time.Millisecond),
		c.readBytes.Load(),
		c.writtenBytes.Load(),
		c.safeLocalAddr(),
		c.safeRemoteAddr(),
		errText,
		c.device.dialStats(),
	)
}

func classifyOutlineIOErr(err error) string {
	if err == nil {
		return "nil"
	}
	msg := err.Error()
	switch {
	case errors.Is(err, net.ErrClosed) || strings.Contains(msg, "use of closed network connection"):
		return "closed"
	case strings.Contains(msg, "chacha20poly1305: message authentication failed"):
		return "aead_auth_failed"
	case strings.Contains(msg, "connection reset by peer"):
		return "reset_by_peer"
	case strings.Contains(msg, "broken pipe"):
		return "broken_pipe"
	case strings.Contains(msg, "EOF"):
		return "eof"
	case strings.Contains(msg, "i/o timeout") || strings.Contains(msg, "deadline exceeded"):
		return "timeout"
	default:
		return "other"
	}
}

func contextCause(ctx context.Context) string {
	if ctx == nil {
		return "<nil>"
	}
	if cause := context.Cause(ctx); cause != nil {
		return cause.Error()
	}
	return "<nil>"
}

func (c *outlineLoggedConn) safeLocalAddr() net.Addr {
	if c.Conn == nil {
		return nil
	}
	return c.Conn.LocalAddr()
}

func (c *outlineLoggedConn) safeRemoteAddr() net.Addr {
	if c.Conn == nil {
		return nil
	}
	return c.Conn.RemoteAddr()
}

type truncatedDNSConn struct {
	req []byte
}

func newTruncatedDNSConn(host string, port int) net.Conn {
	return &truncatedDNSConn{}
}

func (c *truncatedDNSConn) Read(b []byte) (int, error) {

	if len(c.req) < 12 {
		return 0, errors.New("invalid dns packet")
	}

	resp := make([]byte, len(c.req))
	copy(resp, c.req)

	// response
	resp[2] |= 0x80

	// truncated
	resp[2] |= 0x02

	resp[6] = 0
	resp[7] = 0

	n := copy(b, resp)
	return n, nil
}

func (c *truncatedDNSConn) Write(b []byte) (int, error) {
	c.req = make([]byte, len(b))
	copy(c.req, b)
	return len(b), nil
}

func (c *truncatedDNSConn) Close() error                       { return nil }
func (c *truncatedDNSConn) LocalAddr() net.Addr                { return nil }
func (c *truncatedDNSConn) RemoteAddr() net.Addr               { return nil }
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
	return d.proxyAddr
}

func (d *OutlineDevice) Close() error {
	log.Infof("SOCKS5 close requested proxy=%s stats={%s}", d.proxyAddr, d.dialStats())
	d.closeOnce.Do(func() {
		close(d.closed)
	})
	if d.listener != nil {
		return d.listener.Close()
	}
	return nil
}
