package trusttunnel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/BurntSushi/toml"

	"core/auth"
	log "core/log"
	"core/protocol"
	"core/trusttunnel/internal"
	"core/tunnel/protected_dialer"

	tt "trusttunnel-go/manager"
)

type TrustTunnelDevice struct {
	trusttunnelInstance *tt.TrustTunnelManager
	config              map[string]any
	proxyAddr           string
	svrIP               net.IP
}

var protectSocket = protected_dialer.ProtectSocketIntErr

func protectTrustTunnelSocket(fd int) int {
	if err := protectSocket(fd); err != nil {
		log.Errorf("trusttunnel", "[TrustTunnel] socket protection failed: %v", err)
		return 1
	}
	return 0
}

func NewTrustTunnelDevice(accepted map[string]any) (*TrustTunnelDevice, error) {
	serverIPStr, err := internal.ExtractServerIP(accepted)
	if err != nil {
		return nil, fmt.Errorf("failed to extract server IP: %w", err)
	}

	ip := net.ParseIP(serverIPStr)
	if ip == nil {
		return nil, fmt.Errorf("invalid server IP: %q", serverIPStr)
	}

	// Pick a free local port for the SOCKS inbound.
	listenConfig := net.ListenConfig{}
	l, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to allocate local port: %w", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	socksUser := auth.GenerateRandomAuth()
	socksPass := auth.GenerateRandomAuth()

	// Rewrite listener.socks configuration
	parsedConfig := protocol.CloneFields(accepted)

	listenerIface, ok := parsedConfig["listener"]
	if !ok {
		listenerIface = make(map[string]interface{})
		parsedConfig["listener"] = listenerIface
	}
	listener, ok := listenerIface.(map[string]interface{})
	if !ok {
		return nil, errors.New("invalid listener section in config")
	}

	socksIface, ok := listener["socks"]
	if !ok {
		socksIface = make(map[string]interface{})
		listener["socks"] = socksIface
	}
	socks, ok := socksIface.(map[string]interface{})
	if !ok {
		return nil, errors.New("invalid listener.socks section in config")
	}

	socks["address"] = fmt.Sprintf("127.0.0.1:%d", port)
	socks["username"] = socksUser
	socks["password"] = socksPass

	d := &TrustTunnelDevice{
		trusttunnelInstance: tt.NewTrustTunnelManager(),
		config:              parsedConfig,
		proxyAddr:           fmt.Sprintf("%s:%s@127.0.0.1:%d", socksUser, socksPass, port),
		svrIP:               ip,
	}

	d.trusttunnelInstance.SetLogCallback(internal.LogFunc)

	// Register the global socket protection callback
	// This delegates TrustTunnel's OS-level socket protection back to DobbyVPN's `protected_dialer`
	d.trusttunnelInstance.SetProtectSocketCallback(protectTrustTunnelSocket)

	log.Infof("trusttunnel", "[TrustTunnel] SOCKS bridge started")
	return d, nil
}

func (d *TrustTunnelDevice) Open(routingTableID int, uplinkIface string) error {
	if d == nil {
		return errors.New("trusttunnel device is not initialized")
	}

	parsedConfig := d.config

	routingIface, ok := parsedConfig["routing"]
	if !ok {
		routingIface = make(map[string]interface{})
		parsedConfig["routing"] = routingIface
	}
	routingMap, ok := routingIface.(map[string]interface{})
	if !ok {
		return errors.New("invalid routing section in config")
	}

	routingMap["routing_table_id"] = routingTableID
	if uplinkIface != "" {
		routingMap["uplink_interface"] = uplinkIface
	}

	buf := new(bytes.Buffer)
	if err := toml.NewEncoder(buf).Encode(parsedConfig); err != nil {
		return fmt.Errorf("failed to re-encode config with routing: %w", err)
	}
	finalConfig := buf.String()

	loglevel, err := internal.ExtractLogLevel(parsedConfig)
	if err != nil {
		log.Warnf("trusttunnel", "[TrustTunnel] invalid log level, using info: %v", err)
	}
	internal.SetLogLevel(loglevel)
	if err := d.trusttunnelInstance.Start(finalConfig); err != nil {
		return fmt.Errorf("failed to start trusttunnel: %w", err)
	}

	return nil
}

func (d *TrustTunnelDevice) GetServerIP() net.IP {
	if d == nil {
		return nil
	}
	return d.svrIP
}

func (d *TrustTunnelDevice) GetProxyAddr() string {
	if d == nil {
		return ""
	}
	return d.proxyAddr
}

func (d *TrustTunnelDevice) Close() error {
	if d == nil {
		return errors.New("trusttunnel device is not initialized")
	}
	if d.trusttunnelInstance != nil {
		d.trusttunnelInstance.Stop()
		d.trusttunnelInstance = nil
	}
	return nil
}
