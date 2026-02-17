package cloak_outline

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/Jigsaw-Code/outline-sdk/network"
	"github.com/Jigsaw-Code/outline-sdk/network/lwip2transport"
	"github.com/Jigsaw-Code/outline-sdk/transport"
	"github.com/Jigsaw-Code/outline-sdk/x/configurl"
)

const (
	connectivityTestDomain   = "www.google.com"
	connectivityTestResolver = "1.1.1.1:53"
)

type OutlineDevice struct {
	network.IPDevice
	sd    transport.StreamDialer
	pp    *outlinePacketProxy
	svrIP net.IP
}

// Use configurl.NewDefaultProviders() for full transport chain support
var providers = configurl.NewDefaultProviders()

func NewOutlineDevice(transportConfig string) (od *OutlineDevice, err error) {
	defer guard("NewOutlineDevice")()
	ip, err := resolveShadowsocksServerIPFromConfig(transportConfig)
	if err != nil {
		return nil, err
	}
	od = &OutlineDevice{
		svrIP: ip,
	}

	if od.sd, err = providers.NewStreamDialer(context.Background(), transportConfig); err != nil {
		return nil, fmt.Errorf("failed to create TCP dialer: %w", err)
	}

	if od.pp, err = newOutlinePacketProxy(transportConfig); err != nil {
		return nil, fmt.Errorf("failed to create delegate UDP proxy: %w", err)
	}

	if od.IPDevice, err = lwip2transport.ConfigureDevice(od.sd, od.pp); err != nil {
		return nil, fmt.Errorf("failed to configure lwIP: %w", err)
	}

	return od, nil
}

func (d *OutlineDevice) Close() error {
	defer guard("OutlineDevice.Close")()
	return d.IPDevice.Close()
}

func (d *OutlineDevice) Refresh() error {
	defer guard("OutlineDevice.Refresh")()
	return d.pp.testConnectivityAndRefresh(connectivityTestResolver, connectivityTestDomain)
}

func (d *OutlineDevice) GetServerIP() net.IP {
	defer guard("OutlineDevice.GetServerIP")()
	return d.svrIP
}

func (d *OutlineDevice) Read() ([]byte, error) {
	defer guard("OutlineDevice.Read")()
	buf := make([]byte, 65536)
	n, err := d.IPDevice.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to read data: %w", err)
	}
	return buf[:n], nil
}

func (d *OutlineDevice) Write(buf []byte) (int, error) {
	defer guard("OutlineDevice.Write")()
	n, err := d.IPDevice.Write(buf)
	if err != nil {
		return 0, fmt.Errorf("failed to write data: %w", err)
	}
	return n, nil
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

// resolveShadowsocksServerIPFromConfig extracts server IP from transport config
// For WSS configs (tls:sni=...|ws:...|ss://...), it uses the TLS SNI host.
// For plain configs (ss://...), it uses the Shadowsocks host.
func resolveShadowsocksServerIPFromConfig(transportConfig string) (net.IP, error) {
	if transportConfig = strings.TrimSpace(transportConfig); transportConfig == "" {
		return nil, errors.New("config is required")
	}

	var host string

	// First, check for TLS SNI host (used in WSS configs)
	// This is the actual server we connect to for WebSocket over TLS
	if sniHost := extractTLSSNIHost(transportConfig); sniHost != "" {
		host = sniHost
	} else {
		// Fall back to ss:// host for plain Shadowsocks configs
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
			return nil, errors.New("config must contain 'ss://' part")
		}

		parsedURL, err := url.Parse(ssConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to parse ss:// config: %w", err)
		}

		host = strings.TrimSpace(parsedURL.Hostname())
		if host == "" {
			return nil, fmt.Errorf("invalid ss:// config: missing hostname (host part=%q)", parsedURL.Host)
		}
	}

	// Skip resolution for localhost (used when Cloak is enabled)
	if host == "127.0.0.1" || host == "localhost" {
		return net.ParseIP("127.0.0.1").To4(), nil
	}

	ipList, err := net.LookupIP(host)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve server hostname %q: %w", host, err)
	}

	// We Support only IPv4 in this version
	for _, ip := range ipList {
		if ip = ip.To4(); ip != nil {
			return ip, nil
		}
	}
	return nil, errors.New("IPv6 only Shadowsocks server is not supported yet")
}