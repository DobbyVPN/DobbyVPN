package internal

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/Jigsaw-Code/outline-sdk/x/configurl"
	"github.com/armon/go-socks5"
	log "go_client/logger"
)

type OutlineDevice struct {
	listener  net.Listener
	proxyAddr string
	svrIP     net.IP
}

func NewOutlineDevice(transportConfig string) (*OutlineDevice, error) {
	ip, err := ResolveServerIPFromConfig(transportConfig)
	if err != nil {
		return nil, err
	}

	providers := configurl.NewDefaultProviders()

	// TCP транспорт Outline
	sd, err := providers.NewStreamDialer(context.Background(), transportConfig)
	if err != nil {
		return nil, err
	}

	// Создаем SOCKS5 сервер, который перенаправляет трафик в Outline Dialer
	conf := &socks5.Config{
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return sd.DialStream(ctx, addr)
		},
	}
	server, err := socks5.New(conf)
	if err != nil {
		return nil, err
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	addr := listener.Addr().String()
	od := &OutlineDevice{
		listener:  listener,
		proxyAddr: addr,
		svrIP:     ip,
	}

	go func() {
		log.Infof("SOCKS5 server started")
		if err := server.Serve(listener); err != nil {
			log.Infof("SOCKS5 server stopped")
		}
	}()

	return od, nil
}

// extractTLSSNIHost extracts the host from "tls:sni=HOST" part of the config.
// Returns empty string if not found.
func extractTLSSNIHost(transportConfig string) string {
	parts := strings.Split(transportConfig, "|")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "tls:") {
			// Parse tls:sni=HOST or tls:sni=HOST&other_param=value
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

// ResolveServerIPFromConfig extracts and resolves the actual server IP from transport config.
// For WSS configs (tls:sni=...|ws:...|ss://...), it uses the TLS SNI host.
// For plain configs (ss://...), it uses the Shadowsocks host.
// This is exported for use in routing setup before creating connections.
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

	// Skip resolution for localhost (used when Cloak is enabled)
	if host == "127.0.0.1" || host == "localhost" {
		log.Infof("outline client: localhost detected, skipping IP resolution")
		return net.ParseIP("127.0.0.1").To4(), nil
	}

	// Resolve hostname to IP
	ipList, err := net.LookupIP(host)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve server hostname %q: %w", host, err)
	}

	// todo: we only tested IPv4 routing table, need to test IPv6 in the future
	for _, ip := range ipList {
		if ip = ip.To4(); ip != nil {
			log.Infof("outline client: resolved %s -> %s", host, ip.String())
			return ip, nil
		}
	}
	return nil, errors.New("IPv6 only Shadowsocks server is not supported yet")
}

// extractSSHost extracts the host from ss:// part of the config.
func extractSSHost(transportConfig string) (string, error) {
	parts := strings.Split(transportConfig, "|")
	var ssConfig string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "ss://") {
			ssConfig = part
			break
		}
	}

	if ssConfig == "" {
		return "", errors.New("config must contain 'ss://' part")
	}

	parsedURL, err := url.Parse(ssConfig)
	if err != nil {
		return "", fmt.Errorf("failed to parse ss:// config: %w", err)
	}

	host := strings.TrimSpace(parsedURL.Hostname())
	if host == "" {
		return "", fmt.Errorf("invalid ss:// config: missing hostname (host part=%q)", parsedURL.Host)
	}
	return host, nil
}

// resolveShadowsocksServerIPFromConfig is a wrapper for backward compatibility
func resolveShadowsocksServerIPFromConfig(transportConfig string) (net.IP, error) {
	return ResolveServerIPFromConfig(transportConfig)
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
