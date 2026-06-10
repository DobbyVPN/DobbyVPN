package trusttunnel

import (
	"bytes"
	"errors"
	"fmt"
	"net"

	"github.com/BurntSushi/toml"

	"go_module/auth"
	log "go_module/log"
	"go_module/trusttunnel/internal"

	tt "trusttunnel-go/manager"
)

type TrutTunnelDevice struct {
	trusttunnelInstance *tt.TrustTunnelManager
	config              string
	proxyAddr           string
	svrIP               net.IP
	svrPort             int
	socksUser           string
	socksPass           string
}

func NewTrustTunnelDevice(trusttunnelConfig string) (*TrutTunnelDevice, error) {
	serverIPStr, err := internal.ExtractServerIP(trusttunnelConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to extract server IP: %w", err)
	}

	ip := net.ParseIP(serverIPStr)
	if ip == nil {
		return nil, fmt.Errorf("invalid server IP: %q", serverIPStr)
	}

	// Pick a free local port for the SOCKS inbound.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to allocate local port: %w", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	socksUser := auth.GenerateRandomAuth()
	socksPass := auth.GenerateRandomAuth()

	// Rewrite listener.socks configuration
	var parsedConfig map[string]interface{}
	if _, err := toml.Decode(trusttunnelConfig, &parsedConfig); err != nil {
		return nil, fmt.Errorf("failed to decode config for update: %w", err)
	}

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

	buf := new(bytes.Buffer)
	if err := toml.NewEncoder(buf).Encode(parsedConfig); err != nil {
		return nil, fmt.Errorf("failed to re-encode config: %w", err)
	}
	trusttunnelConfig = buf.String()

	d := &TrutTunnelDevice{
		trusttunnelInstance: tt.NewTrustTunnelManager(),
		config:              trusttunnelConfig,
		proxyAddr:           fmt.Sprintf("%s:%s@127.0.0.1:%d", socksUser, socksPass, port),
		svrIP:               ip,
		svrPort:             port,
		socksUser:           socksUser,
		socksPass:           socksPass,
	}

	d.trusttunnelInstance.SetLogCallback(internal.LogFunc)

	log.Infof("[TrustTunnel] SOCKS bridge started at %s (serverIP=%s)", d.proxyAddr, d.svrIP.String())
	return d, nil
}

func (d *TrutTunnelDevice) Open(routingTableID int, uplinkIface string) error {
	if d == nil {
		return errors.New("trusttunnel device is not initialized")
	}

	err := d.trusttunnelInstance.Start(d.config)
	if err != nil {
		d.trusttunnelInstance.Stop()
		return fmt.Errorf("failed to start trusttunnel: %w", err)
	}

	loglevel, err := internal.ExtractLogLevel(d.config)
	if err != nil {
		log.Infof("[TrustTunnel] failed to parse log level, continuing without logs")
	}
	internal.SetLogLever(loglevel)

	return nil
}

func (d *TrutTunnelDevice) GetServerIP() net.IP {
	if d == nil {
		return nil
	}
	return d.svrIP
}

func (d *TrutTunnelDevice) GetProxyAddr() string {
	if d == nil {
		return ""
	}
	return d.proxyAddr
}

func (d *TrutTunnelDevice) Close() error {
	if d == nil {
		return errors.New("trusttunnel device is not initialized")
	}
	if d.trusttunnelInstance != nil {
		d.trusttunnelInstance.Stop()
		d.trusttunnelInstance = nil
	}
	return nil
}
