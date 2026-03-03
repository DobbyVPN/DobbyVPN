package tunnel

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	log "go_client/logger"

	"github.com/xjasonlyu/tun2socks/v2/core"
	"github.com/xjasonlyu/tun2socks/v2/core/adapter"
	"github.com/xjasonlyu/tun2socks/v2/core/device/iobased"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
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

type DobbyTransportHandler struct {
	bypassNetworks []*net.IPNet
	proxyDial      func(ctx context.Context, network, addr string) (net.Conn, error)
}

func NewDobbyHandler(proxyDialer func(context.Context, string, string) (net.Conn, error)) *DobbyTransportHandler {
	handler := &DobbyTransportHandler{
		proxyDial:      proxyDialer,
		bypassNetworks: DefaultBypassCIDRs,
	}

	serverIP := net.ParseIP("85.9.223.19")
	handler.bypassNetworks = append(handler.bypassNetworks, &net.IPNet{IP: serverIP, Mask: net.CIDRMask(32, 32)})

	return handler
}

func (h *DobbyTransportHandler) isBypass(ip net.IP) bool {
	for _, network := range h.bypassNetworks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func (h *DobbyTransportHandler) HandleTCP(conn adapter.TCPConn) {
	defer conn.Close()

	destAddr := conn.RemoteAddr().String()
	host, _, _ := net.SplitHostPort(destAddr)
	destIP := net.ParseIP(host)

	var remoteConn net.Conn
	var err error

	if destIP != nil && h.isBypass(destIP) {
		log.Infof("[Direct] %s", destAddr)
		remoteConn, err = net.DialTimeout("tcp", destAddr, 5*time.Second)
	} else {
		log.Infof("[Proxy] %s", destAddr)
		remoteConn, err = h.proxyDial(context.Background(), "tcp", destAddr)
	}

	if err != nil {
		log.Infof("Failed to dial %s: %v", destAddr, err)
		return
	}
	defer remoteConn.Close()

	// Двусторонняя перекачка (Relay)
	relay(conn, remoteConn)
}

func (h *DobbyTransportHandler) HandleUDP(conn adapter.UDPConn) {
	// Временное решение: пускаем UDP напрямую, чтобы работал DNS
	defer conn.Close()
	destAddr := conn.RemoteAddr().String()

	// Создаем локальный сокет для выхода в мир
	remoteConn, err := net.ListenPacket("udp", ":0")
	if err != nil {
		log.Infof("UDP Direct error: %v", err)
		return
	}
	defer remoteConn.Close()

	target, _ := net.ResolveUDPAddr("udp", destAddr)

	// Перекачка UDP пакетов (упрощенная)
	go func() {
		buf := make([]byte, 2048)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				return
			}
			remoteConn.WriteTo(buf[:n], target)
		}
	}()

	buf := make([]byte, 2048)
	for {
		n, from, err := remoteConn.ReadFrom(buf)
		if err != nil {
			return
		}
		if from.String() == target.String() {
			conn.Write(buf[:n])
		}
	}
}

func relay(left, right net.Conn) {
	go func() {
		io.Copy(right, left)
		if tcpRight, ok := right.(*net.TCPConn); ok {
			tcpRight.CloseWrite()
		} else {
			right.Close()
		}
	}()
	io.Copy(left, right)
}

var (
	activeStack *stack.Stack
	stackMu     sync.Mutex
)

func StartDobbyTunnel(
	tun io.ReadWriteCloser,
	outlineDialer func(context.Context, string, string) (net.Conn, error),
) error {

	stackMu.Lock()
	defer stackMu.Unlock()

	if activeStack != nil {
		return fmt.Errorf("Dobby tunnel already running")
	}

	device, err := iobased.New(tun, 1500, 0)
	if err != nil {
		return err
	}

	handler := NewDobbyHandler(outlineDialer)

	s, err := core.CreateStack(&core.Config{
		LinkEndpoint:     device,
		TransportHandler: handler,
	})
	if err != nil {
		return err
	}

	activeStack = s

	log.Infof("Dobby Tunnel started")

	return nil
}

func StopDobbyTunnel() {
	stackMu.Lock()
	defer stackMu.Unlock()

	if activeStack != nil {
		activeStack.Close()
		activeStack.Wait()
		activeStack = nil
		log.Infof("Dobby Tunnel stopped")
	}
}

func StartTransfer(
	io.ReadWriteCloser,
	any,
	any,
) {
}

func StopTransfer() {
}
