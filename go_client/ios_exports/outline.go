package cloak_outline

import (
	"context"
	"errors"
	"fmt"
	"go_client/common"
	log "go_client/logger"
	"golang.org/x/sys/unix"
	"net"
	"net/url"
	"runtime/debug"
	"strings"

	"github.com/Jigsaw-Code/outline-sdk/network"
	"github.com/Jigsaw-Code/outline-sdk/network/lwip2transport"
	"github.com/Jigsaw-Code/outline-sdk/transport"
	"github.com/Jigsaw-Code/outline-sdk/x/configurl"
)
const utunControlName = "com.apple.net.utun_control"


func guardExport(fnName string) func() {
	return func() {
		if r := recover(); r != nil {
			msg := "panic in " + fnName + ": " + unsafeToString(r)
			log.Infof("%s\n%s", msg, string(debug.Stack()))
		}
	}
}

func unsafeToString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprintf("%v", v)
	}
}

type OutlineDevice struct {
	network.IPDevice
	sd    transport.StreamDialer
	pp    *outlinePacketProxy
	svrIP net.IP
}

// Use configurl.NewDefaultProviders() for full transport chain support
var providers = configurl.NewDefaultProviders()

var client *OutlineDevice

func NewOutlineClient(transportConfig string) error {
	defer guard("NewOutlineDevice")()
	ip, err := resolveShadowsocksServerIPFromConfig(transportConfig)
	if err != nil {
		return err
	}
	client = &OutlineDevice{
		svrIP: ip,
	}

	if client.sd, err = providers.NewStreamDialer(context.Background(), transportConfig); err != nil {
		return fmt.Errorf("failed to create TCP dialer: %w", err)
	}

	if client.pp, err = newOutlinePacketProxy(transportConfig); err != nil {
		return fmt.Errorf("failed to create delegate UDP proxy: %w", err)
	}

	if client.IPDevice, err = lwip2transport.ConfigureDevice(client.sd, client.pp); err != nil {
		return fmt.Errorf("failed to configure lwIP: %w", err)
	}

	return nil
}

func OutlineConnect() error {
	defer guardExport("OutlineConnect")()
	log.Infof("OutlineConnect() called")

	if client == nil {
		return fmt.Errorf("OutlineConnect(): client is nil")
	}
	fd := GetTunnelFileDescriptor()

	common.StartTransfer(
		fd,
		func(buf []byte) (int, error) {
			return client.IPDevice.Read(buf)
		},
		func(buf []byte) (int, error) {
			return client.IPDevice.Write(buf)
		},
	)

	log.Infof("OutlineConnect() finished successfully")
	return nil
}

func OutlineDisconnect() error {
	defer guardExport("OutlineDisconnect")()
	log.Infof("OutlineDisconnect() called")

	if client == nil {
		// это не ошибка — просто нечего отключать
		return nil
	}

	common.StopTransfer()
	client = nil

	log.Infof("OutlineDisconnect() finished")
	return nil
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

func GetTunnelFileDescriptor() int {
	ctlInfo := &unix.CtlInfo{}
	copy(ctlInfo.Name[:], utunControlName)

	for fd := 0; fd < 1024; fd++ {
		addr, err := unix.Getpeername(fd)
		if err != nil {
			continue
		}

		addrCTL, ok := addr.(*unix.SockaddrCtl)
		if !ok {
			continue
		}

		if ctlInfo.Id == 0 {
			if err := unix.IoctlCtlInfo(fd, ctlInfo); err != nil {
				continue
			}
		}

		if addrCTL.ID == ctlInfo.Id {
			return fd
		}
	}

	return -1
}
+

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
