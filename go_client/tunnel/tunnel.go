package tunnel

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/xjasonlyu/tun2socks/v2/engine"
	M "github.com/xjasonlyu/tun2socks/v2/metadata" // Используем алиас M как в proxy.go
	"github.com/xjasonlyu/tun2socks/v2/proxy"      // Пакет, содержащий интерфейс Proxy
	"github.com/xjasonlyu/tun2socks/v2/proxy/proto"
	"github.com/xjasonlyu/tun2socks/v2/tunnel"

	log "go_client/logger"
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
		return p.direct.DialUDP(metadata)
	}
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

func StartEngine(fd int, proxyAddr string) {
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

	currentDialer := tunnel.T().Dialer()
	vpnOutbound, ok := currentDialer.(proxy.Proxy)
	if !ok {
		log.Infof("[Engine] Current dialer is not a proxy")
		return
	}

	directOutbound := proxy.NewDirect()

	wrapper := &DobbyProxy{
		vpn:    vpnOutbound,
		direct: directOutbound,
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
