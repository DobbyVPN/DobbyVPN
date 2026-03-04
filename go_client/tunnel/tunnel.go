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

var (
	activeStack *stack.Stack
	stackMu     sync.Mutex
)

type DobbyTransportHandler struct {
	bypassNetworks []*net.IPNet
	proxyDial      func(ctx context.Context, network, addr string) (net.Conn, error)
}

func NewDobbyHandler(proxyDialer func(context.Context, string, string) (net.Conn, error)) *DobbyTransportHandler {
	handler := &DobbyTransportHandler{
		proxyDial:      proxyDialer,
		bypassNetworks: DefaultBypassCIDRs,
	}

	serverIPStr := "85.9.223.19"
	serverIP := net.ParseIP(serverIPStr)
	handler.bypassNetworks = append(handler.bypassNetworks, &net.IPNet{IP: serverIP, Mask: net.CIDRMask(32, 32)})
	log.Infof("[Dobby] Bypass list initialized. Server IP %s excluded.", serverIPStr)

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
	host, _, err := net.SplitHostPort(destAddr)
	if err != nil {
		log.Infof("[Dobby] [TCP] Invalid dest address %s: %v", destAddr, err)
		return
	}
	destIP := net.ParseIP(host)

	var remoteConn net.Conn

	if destIP != nil && h.isBypass(destIP) {
		log.Infof("[Dobby] [TCP-Direct] %s", destAddr)
		remoteConn, err = net.DialTimeout("tcp", destAddr, 5*time.Second)
	} else {
		log.Infof("[Dobby] [TCP-Proxy] Attempting to dial %s via Outline...", destAddr)
		remoteConn, err = h.proxyDial(context.Background(), "tcp", destAddr)
	}

	if err != nil {
		log.Infof("[Dobby] [TCP-Error] Failed to connect to %s: %v", destAddr, err)
		return
	}
	defer remoteConn.Close()

	log.Infof("[Dobby] [TCP-Relay] Start relaying %s", destAddr)
	relay(conn, remoteConn, destAddr)
	log.Infof("[Dobby] [TCP-Relay] Connection closed for %s", destAddr)
}

func (h *DobbyTransportHandler) HandleUDP(conn adapter.UDPConn) {
	defer conn.Close()
	destAddr := conn.RemoteAddr().String()
	log.Infof("[Dobby] [UDP-Direct] New session to %s", destAddr)

	remoteConn, err := net.ListenPacket("udp", ":0")
	if err != nil {
		log.Infof("[Dobby] [UDP-Error] Failed to listen: %v", err)
		return
	}
	defer remoteConn.Close()

	target, err := net.ResolveUDPAddr("udp", destAddr)
	if err != nil {
		log.Infof("[Dobby] [UDP-Error] Failed to resolve %s: %v", destAddr, err)
		return
	}

	// Канал для отслеживания ошибок в горутине
	errChan := make(chan error, 1)

	go func() {
		buf := make([]byte, 2048)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				errChan <- err
				return
			}
			_, err = remoteConn.WriteTo(buf[:n], target)
			if err != nil {
				errChan <- err
				return
			}
		}
	}()

	buf := make([]byte, 2048)
	for {
		select {
		case err := <-errChan:
			log.Infof("[Dobby] [UDP-Relay] Stopped for %s: %v", destAddr, err)
			return
		default:
			remoteConn.SetReadDeadline(time.Now().Add(time.Second * 30))
			n, from, err := remoteConn.ReadFrom(buf)
			if err != nil {
				log.Infof("[Dobby] [UDP-Timeout/Error] %s: %v", destAddr, err)
				return
			}
			if from.String() == target.String() {
				conn.Write(buf[:n])
			}
		}
	}
}

func relay(left, right net.Conn, addr string) {
	var wg sync.WaitGroup
	wg.Add(2)

	// Копируем из TUN в прокси
	go func() {
		defer wg.Done()
		n, err := io.Copy(right, left)
		log.Infof("[Dobby] [Relay-Sent] %s: %d bytes sent, err: %v", addr, n, err)
		if tcp, ok := right.(*net.TCPConn); ok {
			tcp.CloseWrite()
		} else {
			right.Close()
		}
	}()

	// Копируем из прокси в TUN
	go func() {
		defer wg.Done()
		n, err := io.Copy(left, right)
		log.Infof("[Dobby] [Relay-Recv] %s: %d bytes received, err: %v", addr, n, err)
		left.Close()
	}()

	wg.Wait()
}

// ... (остальные функции StartDobbyTunnel и StopDobbyTunnel с добавленным Info-логированием)

func StartDobbyTunnel(tun io.ReadWriteCloser, outlineDialer func(context.Context, string, string) (net.Conn, error)) error {
	stackMu.Lock()
	defer stackMu.Unlock()

	if activeStack != nil {
		return fmt.Errorf("Dobby tunnel already running")
	}

	log.Infof("[Dobby] Initializing gVisor stack...")
	device, err := iobased.New(tun, 1500, 0)
	if err != nil {
		log.Infof("[Dobby] Failed to create tun device: %v", err)
		return err
	}

	handler := NewDobbyHandler(outlineDialer)
	s, err := core.CreateStack(&core.Config{
		LinkEndpoint:     device,
		TransportHandler: handler,
	})
	if err != nil {
		log.Infof("[Dobby] Failed to create stack: %v", err)
		return err
	}

	activeStack = s
	log.Infof("[Dobby] Tunnel successfully started and stack is active")
	return nil
}
