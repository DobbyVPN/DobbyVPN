package cloak_outline

import (
	"context"
	"errors"
	"fmt"
	"github.com/Jigsaw-Code/outline-sdk/network"
	"github.com/Jigsaw-Code/outline-sdk/network/lwip2transport"
	"github.com/Jigsaw-Code/outline-sdk/transport"
	"github.com/Jigsaw-Code/outline-sdk/x/configurl"
	"golang.org/x/sys/unix"
	"net"
	"net/url"
	"strings"
	"sync"
)

type OutlineDevice struct {
	dev    network.IPDevice
	sd     transport.StreamDialer
	pp     *outlinePacketProxy
	svrIP  net.IP
	ctx    context.Context
	fd     int
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Use configurl.NewDefaultProviders() for full transport chain support
var providers = configurl.NewDefaultProviders()
var client *OutlineDevice

func NewOutlineDevice(transportConfig string, fd int) (err error) {
	defer guard("NewOutlineDevice")()
	ctx, cancel := context.WithCancel(context.Background())
	ip, err := resolveShadowsocksServerIPFromConfig(transportConfig)
	if err != nil {
		cancel()
		return err
	}
	client = &OutlineDevice{
		svrIP:  ip,
		fd:     fd,
		ctx:    ctx,
		cancel: cancel,
	}

	if client.sd, err = providers.NewStreamDialer(context.Background(), transportConfig); err != nil {
		cancel()
		return fmt.Errorf("failed to create TCP dialer: %w", err)
	}

	if client.pp, err = newOutlinePacketProxy(transportConfig); err != nil {
		cancel()
		return fmt.Errorf("failed to create delegate UDP proxy: %w", err)
	}

	if client.dev, err = lwip2transport.ConfigureDevice(client.sd, client.pp); err != nil {
		cancel()
		return fmt.Errorf("failed to configure lwIP: %w", err)
	}

	return nil
}

func Connect() error {
	client.wg.Add(1)
	go func() {
		defer client.wg.Done()
		buf := make([]byte, 65536)
		for {
			select {
			case <-client.ctx.Done():
				return
			default:
			}

			n, err := unix.Read(client.fd, buf)
			if err != nil {
				return
			}
			_, err = client.dev.Write(buf[:n])
			if err != nil {
				return
			}
		}
	}()

	// lwip → TUN
	client.wg.Add(1)
	go func() {
		defer client.wg.Done()
		buf := make([]byte, 65536)
		for {
			select {
			case <-client.ctx.Done():
				return
			default:
			}

			n, err := client.dev.Read(buf)
			if err != nil {
				return
			}
			_, err = unix.Write(client.fd, buf[:n])
			if err != nil {
				return
			}
		}
	}()
	return nil
}

func Disconnect() error {
	client.cancel()
	client.wg.Wait()
	if client.dev != nil {
		_ = client.dev.Close()
	}
	return nil
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
