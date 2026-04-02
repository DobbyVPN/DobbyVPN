package tunnel

import (
	"context"
	"fmt"
	"github.com/xjasonlyu/tun2socks/v2/engine"
	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	"github.com/xjasonlyu/tun2socks/v2/proxy"
	"github.com/xjasonlyu/tun2socks/v2/proxy/proto"
	"github.com/xjasonlyu/tun2socks/v2/tunnel"
	"go_client/log"
	"go_client/tunnel/protected_dialer"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var (
	transferMu sync.Mutex
	isRunning  bool

	routesMu sync.RWMutex
)

type DobbyProxy struct {
	vpn    proxy.Proxy
	direct proxy.Proxy
}

func (p *DobbyProxy) DialContext(ctx context.Context, metadata *M.Metadata) (net.Conn, error) {
	if IsBypass(metadata) {
		log.Infof("[Router] Using DIRECT for %s", metadata.DstIP)
		return p.direct.DialContext(ctx, metadata)
	}
	log.Infof("[Router] Using VPN for %s", metadata.DstIP)
	return p.vpn.DialContext(ctx, metadata)
}

func (p *DobbyProxy) DialUDP(metadata *M.Metadata) (net.PacketConn, error) {
	if IsBypass(metadata) {
		log.Infof("[Router] Using UDP DIRECT for %s", metadata.DstIP)
		return p.direct.DialUDP(metadata)
	}
	log.Infof("[Router] Using UDP VPN for %s", metadata.DstIP)
	return p.vpn.DialUDP(metadata)
}

func (p *DobbyProxy) Addr() string {
	return p.vpn.Addr()
}

func (p *DobbyProxy) Proto() proto.Proto {
	return p.vpn.Proto()
}

func waitForWintun(timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		ifaces, _ := net.Interfaces()

		for _, ifc := range ifaces {
			if strings.Contains(strings.ToLower(ifc.Name), "wintun") {
				return ifc.Name, nil
			}
		}

		time.Sleep(300 * time.Millisecond)
	}

	return "", fmt.Errorf("wintun not found")
}

func setInterfaceAddress(name, ip string) error {
	var lastErr error

	for i := 0; i < 5; i++ {
		cmd := exec.Command(
			"netsh", "interface", "ipv4", "set", "address",
			fmt.Sprintf(`name=%s`, name),
			"source=static",
			fmt.Sprintf(`addr=%s`, ip),
			"mask=255.255.255.0",
		)

		out, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}

		lastErr = fmt.Errorf("attempt %d failed: %w %s", i, err, out)
		time.Sleep(300 * time.Millisecond)
	}

	return lastErr
}

func setDNS(name, dns string) error {
	cmd := exec.Command(
		"netsh", "interface", "ipv4", "set", "dnsservers",
		fmt.Sprintf(`name=%s`, name),
		"static", dns,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("set dns failed: %w %s", err, out)
	}
	return nil
}

func StartEngineDarwin(proxyAddr string) (string, error) {
	log.Infof("[Engine] StartEngineDarwin proxy=%s", proxyAddr)

	transferMu.Lock()
	defer transferMu.Unlock()

	if isRunning {
		stopLocked()
	}

	proxyURL := fmt.Sprintf("socks5://%s", proxyAddr)
	deviceName := "utun233"

	key := &engine.Key{
		Proxy:    proxyURL,
		Device:   deviceName,
		LogLevel: "info",
		MTU:      1500,
	}

	engine.Insert(key)

	log.Infof("[Engine] Starting tun2socks (utun mode)...")
	engine.Start()

	time.Sleep(500 * time.Millisecond)

	// Проверка интерфейса
	ifaces, _ := net.Interfaces()
	found := false
	for _, ifc := range ifaces {
		if ifc.Name == deviceName {
			found = true
			break
		}
	}

	if !found {
		engine.Stop()
		return "", fmt.Errorf("utun interface not found: %s", deviceName)
	}

	log.Infof("[Engine] utun created: %s", deviceName)

	cmd := exec.Command(
		"ifconfig",
		deviceName,
		"inet",
		"198.18.0.1",
		"198.18.0.2",
		"netmask",
		"255.255.0.0",
		"up",
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		engine.Stop()
		return "", fmt.Errorf("ifconfig failed: %w (%s)", err, out)
	}

	log.Infof("[Engine] utun configured: %s", deviceName)

	setTunnelRouting()

	log.Infof("[Engine] tun2socks started (darwin utun mode)")

	return deviceName, nil
}

func StartEngineWindows(proxyAddr string, uplinkIface string) {
	log.Infof("[Engine] StartEngineDesktop proxy=%s iface=%s", proxyAddr, uplinkIface)

	transferMu.Lock()
	defer transferMu.Unlock()

	if isRunning {
		stopLocked()
	}

	proxyURL := fmt.Sprintf("socks5://%s", proxyAddr)

	key := &engine.Key{
		Proxy:     proxyURL,
		Device:    "wintun",
		Interface: uplinkIface,
		LogLevel:  "info",
		MTU:       1500,
	}

	engine.Insert(key)

	log.Infof("[Engine] Starting tun2socks engine...")
	engine.Start()

	ifName, err := waitForWintun(5 * time.Second)
	if err != nil {
		log.Infof("[Engine] wintun not found: %v", err)
		engine.Stop()
		return
	}

	log.Infof("[Engine] Found Wintun interface: %s", ifName)

	if err := setInterfaceAddress(ifName, "10.0.85.2"); err != nil {
		log.Infof("[Engine] failed to set interface address: %v", err)
		engine.Stop()
		return
	}

	if err := setDNS(ifName, "1.1.1.1"); err != nil {
		log.Infof("[Engine] failed to set DNS: %v", err)
		engine.Stop()
		return
	}

	log.Infof("[Engine] Wintun configured: %s", ifName)

	setTunnelRouting()
}

func StartEngineLinuxBased(fd int, proxyAddr string) {
	transferMu.Lock()
	defer transferMu.Unlock()

	if isRunning {
		stopLocked()
	}

	devicePath := fmt.Sprintf("fd://%d", fd)
	proxyURL := fmt.Sprintf("socks5://%s", proxyAddr)

	log.Infof("[Engine] Starting Dobby with FD: %d", fd)

	key := &engine.Key{
		Proxy:    proxyURL,
		Device:   devicePath,
		LogLevel: "info",
		MTU:      1500,
	}

	engine.Insert(key)
	engine.Start()

	setTunnelRouting()
}

func setTunnelRouting() {

	currentDialer := tunnel.T().Dialer()
	vpnOutbound, ok := currentDialer.(proxy.Proxy)
	if !ok {
		log.Infof("[Engine] Current dialer is not a proxy")
		return
	}

	directOutbound := &protected_dialer.ProtectedDirectProxy{
		Proxy: proxy.NewDirect(),
	}

	wrapper := &DobbyProxy{
		vpn:    vpnOutbound,
		direct: directOutbound,
	}

	if tunnel.T() == nil {
		log.Infof("tunnel.T() return nil")
	}

	tunnel.T().SetDialer(wrapper)

	isRunning = true
}

func StopEngine() {
	transferMu.Lock()
	defer transferMu.Unlock()
	if isRunning {
		stopLocked()
	}
}

func stopLocked() {
	engine.Stop()
	isRunning = false
}

type SocketProtector interface {
	Protect(fd int) bool
}
