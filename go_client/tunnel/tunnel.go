package tunnel

import "C"
import (
	"context"
	"fmt"
	"github.com/xjasonlyu/tun2socks/v2/engine"
	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	"github.com/xjasonlyu/tun2socks/v2/proxy"
	"github.com/xjasonlyu/tun2socks/v2/proxy/proto"
	"github.com/xjasonlyu/tun2socks/v2/tunnel"
	"go_client/log"
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

func mustCIDR(s string) *net.IPNet {
	_, ipnet, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return ipnet
}

var DefaultBypassCIDRs = []*net.IPNet{
	mustCIDR("103.21.244.0/22"),
	mustCIDR("103.22.200.0/22"),
	mustCIDR("103.31.4.0/22"),
	mustCIDR("104.16.0.0/13"),
	mustCIDR("104.24.0.0/14"),
	mustCIDR("108.162.192.0/18"),
	mustCIDR("131.0.72.0/22"),
	mustCIDR("141.101.64.0/18"),
	mustCIDR("162.158.0.0/15"),
	mustCIDR("172.64.0.0/13"),
	mustCIDR("173.245.48.0/20"),
	mustCIDR("188.114.96.0/20"),
	mustCIDR("190.93.240.0/20"),
	mustCIDR("197.234.240.0/22"),
	mustCIDR("198.41.128.0/17"),
}

func ResolveHostToCIDRs(host string) []*net.IPNet {
	ips, err := net.LookupIP(host)
	if err != nil {
		log.Infof("[Bypass] resolve failed for %s: %v", host, err)
		return nil
	}

	var result []*net.IPNet
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			_, n, _ := net.ParseCIDR(v4.String() + "/32")
			result = append(result, n)
			continue
		}
		_, n, _ := net.ParseCIDR(ip.String() + "/128")
		result = append(result, n)
	}
	return result
}
func AddBypassHost(host string) {
	cidrs := ResolveHostToCIDRs(host)
	if len(cidrs) == 0 {
		log.Infof("[Bypass] no IPs resolved for %s", host)
		return
	}

	routesMu.Lock()
	defer routesMu.Unlock()

	DefaultBypassCIDRs = append(DefaultBypassCIDRs, cidrs...)

	for _, c := range cidrs {
		log.Infof("[Bypass] added %s for host %s", c.String(), host)
	}
}

// isBypass проверяет метаданные соединения на попадание в список исключений
func isBypass(metadata *M.Metadata) bool {
	if metadata == nil {
		return false
	}

	// Используем DestinationIP() из метаданных (структура M)
	destIP := metadata.DstIP
	if !destIP.IsValid() {
		return false
	}

	routesMu.RLock()
	defer routesMu.RUnlock()

	// Конвертируем netip.Addr в стандартный net.IP для проверки
	stdIP := net.IP(destIP.AsSlice())

	for _, route := range DefaultBypassCIDRs {
		if route.Contains(stdIP) {
			log.Infof("[Router] BYPASS hit for IP: %s", stdIP)
			return true
		}
	}
	log.Infof("[Router] PROXY route for IP: %s", stdIP)
	return false
}

// --- Реализация DobbyProxy (Диспетчер) ---

// DobbyProxy реализует интерфейс proxy.Proxy
type DobbyProxy struct {
	vpn    proxy.Proxy // Основной прокси (например, Shadowsocks или Socks5)
	direct proxy.Proxy // Прямое соединение (Direct)
}

func (p *DobbyProxy) DialContext(ctx context.Context, metadata *M.Metadata) (net.Conn, error) {
	if isBypass(metadata) {
		log.Infof("[Router] Using DIRECT for %s", metadata.DstIP)
		return p.direct.DialContext(ctx, metadata)
	}
	log.Infof("[Router] Using VPN for %s", metadata.DstIP)
	return p.vpn.DialContext(ctx, metadata)
}

// DialUDP выбирает исходящий путь для UDP
func (p *DobbyProxy) DialUDP(metadata *M.Metadata) (net.PacketConn, error) {
	if isBypass(metadata) {
		log.Infof("[Router] Using UDP DIRECT for %s", metadata.DstIP)
		return p.direct.DialUDP(metadata)
	}
	log.Infof("[Router] Using UDP VPN for %s", metadata.DstIP)
	return p.vpn.DialUDP(metadata)
}

// Addr возвращает адрес VPN прокси
func (p *DobbyProxy) Addr() string {
	return p.vpn.Addr()
}

// Proto возвращает кастомный протокол или протокол VPN
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

func StartEngineLinux(fd int, proxyAddr string) error {
	transferMu.Lock()
	defer transferMu.Unlock()

	if isRunning {
		stopLocked()
	}

	devicePath := fmt.Sprintf("fd://%d", fd)
	proxyURL := fmt.Sprintf("socks5://%s", proxyAddr)

	log.Infof("[Engine][Linux] Starting tun2socks with FD: %d", fd)

	key := &engine.Key{
		Proxy:    proxyURL,
		Device:   devicePath,
		LogLevel: "info",
		MTU:      1500,
	}

	engine.Insert(key)
	engine.Start()

	if tunnel.T() == nil {
		return fmt.Errorf("tunnel.T() returned nil")
	}

	currentDialer := tunnel.T().Dialer()
	vpnOutbound, ok := currentDialer.(proxy.Proxy)
	if !ok {
		return fmt.Errorf("current dialer is not proxy")
	}

	directOutbound := &ProtectedDirectProxy{
		Proxy: proxy.NewDirect(),
	}

	wrapper := &DobbyProxy{
		vpn:    vpnOutbound,
		direct: directOutbound,
	}

	AddBypassHost("api.ipify.org")

	tunnel.T().SetDialer(wrapper)
	isRunning = true

	log.Infof("[Engine][Linux] tun2socks started successfully")
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

	currentDialer := tunnel.T().Dialer()
	vpnOutbound, ok := currentDialer.(proxy.Proxy)
	if !ok {
		engine.Stop()
		return "", fmt.Errorf("dialer is not proxy")
	}

	directOutbound := &ProtectedDirectProxy{
		Proxy: proxy.NewDirect(),
	}

	wrapper := &DobbyProxy{
		vpn:    vpnOutbound,
		direct: directOutbound,
	}

	AddBypassHost("api.ipify.org")

	tunnel.T().SetDialer(wrapper)

	isRunning = true

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

	currentDialer := tunnel.T().Dialer()
	vpnOutbound, ok := currentDialer.(proxy.Proxy)
	if !ok {
		log.Infof("[Engine] Current dialer is not proxy")
		engine.Stop()
		return
	}

	directOutbound := &ProtectedDirectProxy{
		Proxy: proxy.NewDirect(),
	}

	wrapper := &DobbyProxy{
		vpn:    vpnOutbound,
		direct: directOutbound,
	}

	AddBypassHost("api.ipify.org")

	tunnel.T().SetDialer(wrapper)

	isRunning = true

	log.Infof("[Engine] tun2socks started successfully")
}

func StartEngine(fd int, proxyAddr string) {
	transferMu.Lock()
	defer transferMu.Unlock()

	if isRunning {
		stopLocked()
	}

	// Конфигурируем tun2socks с DEBUG логами
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

	currentDialer := tunnel.T().Dialer()
	vpnOutbound, ok := currentDialer.(proxy.Proxy)
	if !ok {
		log.Infof("[Engine] Current dialer is not a proxy")
		return
	}

	directOutbound := &ProtectedDirectProxy{
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

	engine.Start()

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

// SocketProtector — интерфейс для вызова VpnService.protect(fd) из Kotlin
type SocketProtector interface {
	Protect(fd int) bool
}

var globalProtector SocketProtector

func RegisterProtector(p SocketProtector) {
	globalProtector = p
}

// ProtectedDirectProxy реализует интерфейс proxy.Proxy
type ProtectedDirectProxy struct {
	// Встраиваем стандартный direct, чтобы иметь доступ к методам типа Addr() и Proto()
	proxy.Proxy
}

type ProtectDialer func(ctx context.Context, network, address string) (net.Conn, error)
type ProtectPacketDialer func(ctx context.Context, network, address string) (net.PacketConn, error)

var CustomProtectedDialer ProtectDialer
var CustomProtectedPacketDialer ProtectPacketDialer

// Теперь обновляем твой прокси, чтобы он вызывал эту переменную
func (p *ProtectedDirectProxy) DialContext(ctx context.Context, metadata *M.Metadata) (net.Conn, error) {
	network := metadata.Network.String()
	address := metadata.DestinationAddress()

	// Если функция установлена — используем её, иначе — обычный Direct
	if CustomProtectedDialer != nil {
		log.Infof("[Router] Direct dialing %s via %s (PROTECTED)", address, network)
		return CustomProtectedDialer(ctx, network, address)
	}

	log.Infof("[Router] Direct dialing %s (NO PROTECTION)", address)
	return p.Proxy.DialContext(ctx, metadata)
}

func (p *ProtectedDirectProxy) DialUDP(metadata *M.Metadata) (net.PacketConn, error) {
	network := metadata.Network.String()
	address := metadata.DestinationAddress()

	if CustomProtectedPacketDialer != nil {
		log.Infof("[Router] Direct UDP dialing %s (PROTECTED)", address)
		return CustomProtectedPacketDialer(context.Background(), network, address)
	}

	return p.Proxy.DialUDP(metadata)
}
