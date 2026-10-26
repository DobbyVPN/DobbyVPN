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
	"core/common"
	"core/dnscache"
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

var protectSocket = protected_dialer.ProtectSocketDestinations

func protectTrustTunnelSocket(ctx context.Context, addresses []string, fd int) int {
	if err := protectSocket(ctx, fd, addresses); err != nil {
		log.Errorf("trusttunnel", "[TrustTunnel] socket protection failed: %v", err)
		return 1
	}
	return 0
}

func NewTrustTunnelDevice(ctx context.Context, accepted map[string]any, cache *dnscache.Cache) (*TrustTunnelDevice, error) {
	startedAt := time.Now()
	parsedConfig := protocol.CloneFields(accepted)
	endpoint, ok := parsedConfig["endpoint"].(map[string]any)
	if !ok {
		return nil, errors.New("invalid endpoint configuration")
	}
	addresses, ip, err := internal.PrepareEndpointAddresses(ctx, endpoint, cache.ResolveAddresses)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare TrustTunnel endpoint addresses: %w", err)
	}
	upstreamIPs, err := numericEndpointIPs(addresses)
	if err != nil {
		return nil, fmt.Errorf("failed to retain numeric TrustTunnel endpoint addresses: %w", err)
	}
	endpoint["addresses"] = addresses
	// Native reconnect must not fall back to direct application/DNS traffic
	// before the shared health monitor retires this attempt. Network lifetime
	// and configured bypass remain owned by the shared packet router.
	parsedConfig["killswitch_enabled"] = true
	parsedConfig["killswitch_allow_ports"] = []any{}
	dns := common.VPNDNSServers()
	dnsUpstreams := make([]any, len(dns))
	for i, server := range dns {
		dnsUpstreams[i] = net.JoinHostPort(server, "53")
	}
	endpoint["dns_upstreams"] = dnsUpstreams
	delete(parsedConfig, "dns_upstreams") // Superseded legacy native DNS field.

	// Pick a free local port for the SOCKS inbound.
	listenConfig := net.ListenConfig{}
	l, err := listenConfig.Listen(ctx, "tcp", "127.0.0.1:0")
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

	numericAddresses := make([]string, len(addresses))
	for i, address := range addresses {
		numericAddresses[i] = address.(string)
	}
	// The callback captures this attempt, rather than consulting a mutable
	// "current generation" when native code eventually creates a socket.
	d.trusttunnelInstance.SetProtectSocketCallback(func(fd int) int {
		return protectTrustTunnelSocket(ctx, numericAddresses, fd)
	})

	endpointHostname, _ := endpoint["hostname"].(string)
	log.Info("trusttunnel", "TrustTunnel endpoint prepared for native start", map[string]any{
		"endpoint_hostname":    endpointHostname,
		"endpoint_addresses":   addresses,
		"selected_address":     ip.String(),
		"socks_address":        socksAddress,
		"socks_listener_state": "not_started",
		"preparation_elapsed":  time.Since(startedAt).Truncate(time.Millisecond).String(),
		"vpn_dns_servers":      dns,
		"direct_fallback":      false,
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
		"selected_address":         d.svrIP.String(),
		"socks_address":            d.socksAddress,
		"native_start_result":      "accepted",
		"socks_listener_readiness": "unobserved",
		"start_elapsed":            time.Since(startedAt).Truncate(time.Millisecond).String(),
	})

	return nil
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
