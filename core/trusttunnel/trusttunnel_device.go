package trusttunnel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

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
	socksAddress        string
	svrIP               net.IP
	upstreamIPs         []net.IP
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
	startedAt := time.Now()
	parsedConfig := protocol.CloneFields(accepted)
	endpoint, ok := parsedConfig["endpoint"].(map[string]any)
	if !ok {
		return nil, errors.New("invalid endpoint configuration")
	}
	addresses, ip, err := internal.PrepareEndpointAddresses(endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare TrustTunnel endpoint addresses: %w", err)
	}
	upstreamIPs, err := numericEndpointIPs(addresses)
	if err != nil {
		return nil, fmt.Errorf("failed to retain numeric TrustTunnel endpoint addresses: %w", err)
	}
	endpoint["addresses"] = addresses

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

	socksAddress := fmt.Sprintf("127.0.0.1:%d", port)
	socks["address"] = socksAddress
	socks["username"] = socksUser
	socks["password"] = socksPass

	d := &TrustTunnelDevice{
		trusttunnelInstance: tt.NewTrustTunnelManager(),
		config:              parsedConfig,
		proxyAddr:           fmt.Sprintf("%s:%s@127.0.0.1:%d", socksUser, socksPass, port),
		socksAddress:        socksAddress,
		svrIP:               ip,
		upstreamIPs:         upstreamIPs,
	}

	d.trusttunnelInstance.SetLogCallback(internal.LogFunc)

	// Register the global socket protection callback
	// This delegates TrustTunnel's OS-level socket protection back to DobbyVPN's `protected_dialer`
	d.trusttunnelInstance.SetProtectSocketCallback(protectTrustTunnelSocket)

	endpointHostname, _ := endpoint["hostname"].(string)
	log.Info("trusttunnel", "TrustTunnel endpoint prepared for native start", map[string]any{
		"endpoint_hostname":    endpointHostname,
		"endpoint_addresses":   addresses,
		"selected_ipv4":        ip.String(),
		"socks_address":        socksAddress,
		"socks_listener_state": "not_started",
		"preparation_elapsed":  time.Since(startedAt).Truncate(time.Millisecond).String(),
	})
	return d, nil
}

func numericEndpointIPs(addresses []any) ([]net.IP, error) {
	ips := make([]net.IP, 0, len(addresses))
	for index, value := range addresses {
		address, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("endpoint address[%d] is not a string", index)
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			host = strings.Trim(address, "[]")
		}
		ip, err := netip.ParseAddr(host)
		if err != nil {
			return nil, fmt.Errorf("endpoint address[%d] %q is not numeric: %w", index, address, err)
		}
		ips = append(ips, net.IP(ip.AsSlice()))
	}
	if len(ips) == 0 {
		return nil, errors.New("no numeric endpoint addresses")
	}
	return ips, nil
}

func (d *TrustTunnelDevice) Open(routingTableID int, uplinkIface string) error {
	if d == nil {
		return errors.New("trusttunnel device is not initialized")
	}

	startedAt := time.Now()
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

	endpoint, _ := parsedConfig["endpoint"].(map[string]any)
	endpointHostname, _ := endpoint["hostname"].(string)
	log.Info("trusttunnel", "TrustTunnel native start accepted; local SOCKS listener readiness is not observed here", map[string]any{
		"endpoint_hostname":        endpointHostname,
		"selected_ipv4":            d.svrIP.String(),
		"socks_address":            d.socksAddress,
		"native_start_result":      "accepted",
		"socks_listener_readiness": "unobserved",
		"start_elapsed":            time.Since(startedAt).Truncate(time.Millisecond).String(),
	})

	return nil
}

func (d *TrustTunnelDevice) GetServerIP() net.IP {
	if d == nil {
		return nil
	}
	return d.svrIP
}

// UpstreamIPs returns the numeric addresses TrustTunnel can use for its native
// upstream connection. The returned slices do not expose the device's state.
func (d *TrustTunnelDevice) UpstreamIPs() []net.IP {
	if d == nil {
		return nil
	}
	ips := make([]net.IP, len(d.upstreamIPs))
	for index, ip := range d.upstreamIPs {
		ips[index] = append(net.IP(nil), ip...)
	}
	return ips
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
