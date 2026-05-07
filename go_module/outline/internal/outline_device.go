package internal

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
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

	tcpDialAttempt  atomic.Uint64
	tcpDialOK       atomic.Uint64
	tcpDialErr      atomic.Uint64
	udpDialAttempt  atomic.Uint64
	udpDialOK       atomic.Uint64
	udpDialErr      atomic.Uint64
	udpDNSTruncated atomic.Uint64
	unsupportedDial atomic.Uint64
}

func NewOutlineDevice(transportConfig string) (*OutlineDevice, error) {
	ip, err := ResolveServerIPFromConfig(transportConfig)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	providers := configurl.NewDefaultProviders()

	sd, err := providers.NewStreamDialer(ctx, transportConfig)
	if err != nil {
		return nil, err
	}

	pd, err := providers.NewPacketDialer(ctx, transportConfig)
	if err != nil {
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

	go func() {
		log.Infof("SOCKS5 started on %s", od.proxyAddr)
		if err := server.Serve(listener); err != nil {
			if errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "use of closed network connection") {
				log.Infof("SOCKS5 stopped on %s: closed", od.proxyAddr)
			} else {
				log.Infof("SOCKS5 stopped unexpectedly on %s: %v", od.proxyAddr, err)
			}
		}
	}()

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

func (d *OutlineDevice) handleDial(ctx context.Context, network, addr string) (net.Conn, error) {

	start := time.Now()
	serverIP := "<nil>"
	if d.svrIP != nil {
		serverIP = d.svrIP.String()
	}

	log.Infof(
		"[SOCKS5] dial network=%s addr=%s via server=%s websocket=%v tcpPath=%v udpPath=%v cloak=%v stats={%s}",
		network,
		addr,
		serverIP,
		d.websocket,
		d.hasTCPPath,
		d.hasUDPPath,
		d.useCloak,
		d.dialStats(),
	)

	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)

	switch network {

	case "tcp":
		attempt := d.tcpDialAttempt.Add(1)
		conn, err := d.streamDialer.DialStream(ctx, addr)
		if err != nil {
			d.tcpDialErr.Add(1)
			log.Infof("[SOCKS5 TCP ERROR] attempt=%d dst=%s server=%s elapsed=%s stats={%s} err=%v", attempt, addr, serverIP, time.Since(start), d.dialStats(), err)
			return nil, err
		}

		d.tcpDialOK.Add(1)
		log.Infof("[SOCKS5 TCP OK] attempt=%d dst=%s server=%s elapsed=%s local=%s remote=%s stats={%s}", attempt, addr, serverIP, time.Since(start), conn.LocalAddr(), conn.RemoteAddr(), d.dialStats())
		return conn, nil

	case "udp":
		attempt := d.udpDialAttempt.Add(1)

		// DNS fallback for Cloak
		if d.useCloak && port == 53 {
			d.udpDNSTruncated.Add(1)

			log.Infof("[SOCKS5 DNS] returning truncated DNS attempt=%d addr=%s cloak=%v stats={%s}", attempt, addr, d.useCloak, d.dialStats())

			return newTruncatedDNSConn(host, port), nil
		}

		conn, err := d.packetDialer.DialPacket(ctx, addr)
		if err != nil {
			d.udpDialErr.Add(1)
			log.Infof("[SOCKS5 UDP ERROR] attempt=%d dst=%s server=%s elapsed=%s stats={%s} err=%v", attempt, addr, serverIP, time.Since(start), d.dialStats(), err)
			return nil, err
		}

		d.udpDialOK.Add(1)
		log.Infof("[SOCKS5 UDP OK] attempt=%d dst=%s server=%s elapsed=%s local=%s stats={%s}", attempt, addr, serverIP, time.Since(start), conn.LocalAddr(), d.dialStats())
		return conn, nil
	}

	err := fmt.Errorf("unsupported network %s", network)
	d.unsupportedDial.Add(1)
	log.Infof("[SOCKS5 ERROR] dst=%s server=%s elapsed=%s err=%v", addr, serverIP, time.Since(start), err)
	return nil, err
}

func (d *OutlineDevice) dialStats() string {
	return fmt.Sprintf(
		"uptime=%s tcp=%d/%d/%d udp=%d/%d/%d udpDNSTrunc=%d unsupported=%d",
		time.Since(d.startedAt).Truncate(time.Millisecond),
		d.tcpDialAttempt.Load(),
		d.tcpDialOK.Load(),
		d.tcpDialErr.Load(),
		d.udpDialAttempt.Load(),
		d.udpDialOK.Load(),
		d.udpDialErr.Load(),
		d.udpDNSTruncated.Load(),
		d.unsupportedDial.Load(),
	)
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
	ctx := context.Background()

	ipList, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
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
	if d.listener != nil {
		return d.listener.Close()
	}
	return nil
}
