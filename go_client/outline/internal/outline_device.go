package internal

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Jigsaw-Code/outline-sdk/transport"
	"github.com/Jigsaw-Code/outline-sdk/x/configurl"
	socks5 "github.com/things-go/go-socks5"

	"go_client/log"
)

type OutlineDevice struct {
	listener     net.Listener
	proxyAddr    string
	svrIP        net.IP
	streamDialer transport.StreamDialer
	packetDialer transport.PacketDialer
	useCloak     bool
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

	log.Infof("outline client: cloak mode = %v", useCloak)

	od := &OutlineDevice{
		svrIP:        ip,
		streamDialer: sd,
		packetDialer: pd,
		useCloak:     useCloak,
	}

	server := socks5.NewServer(
		socks5.WithDial(od.handleDial),
	)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	od.listener = listener
	od.proxyAddr = listener.Addr().String()

	go func() {
		log.Infof("SOCKS5 started on %s", od.proxyAddr)
		if err := server.Serve(listener); err != nil {
			log.Infof("SOCKS5 stopped: %v", err)
		}
	}()

	return od, nil
}

func (d *OutlineDevice) handleDial(ctx context.Context, network, addr string) (net.Conn, error) {

	log.Infof("[SOCKS5] dial %s %s", network, addr)

	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)

	switch network {

	case "tcp":

		conn, err := d.streamDialer.DialStream(ctx, addr)
		if err != nil {
			log.Infof("[SOCKS5 TCP ERROR] %v", err)
			return nil, err
		}

		log.Infof("[SOCKS5 TCP OK] %s", addr)
		return conn, nil

	case "udp":

		// DNS fallback for Cloak
		if d.useCloak && port == 53 {

			log.Infof("[SOCKS5 DNS] returning truncated DNS (cloak mode)")

			return newTruncatedDNSConn(host, port), nil
		}

		conn, err := d.packetDialer.DialPacket(ctx, addr)
		if err != nil {
			log.Infof("[SOCKS5 UDP ERROR] %v", err)
			return nil, err
		}

		log.Infof("[SOCKS5 UDP OK] %s", addr)
		return conn, nil
	}

	return nil, fmt.Errorf("unsupported network %s", network)
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

	// QR = response
	resp[2] |= 0x80

	// TC = truncated
	resp[2] |= 0x02

	resp[6] = 0
	resp[7] = 0

	copy(b, resp)

	return len(resp), nil
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

	ipList, err := net.LookupIP(host)
	if err != nil {
		return nil, err
	}

	for _, ip := range ipList {
		if ip = ip.To4(); ip != nil {
			log.Infof("outline client: resolved %s -> %s", host, ip.String())
			return ip, nil
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
	if d.listener != nil {
		return d.listener.Close()
	}
	return nil
}
