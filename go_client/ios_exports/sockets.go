package cloak_outline

import (
	"context"
	log "go_client/logger"
	"go_client/tunnel"
	"net"
	"syscall"
)

// Константы для iOS, чтобы не импортировать тяжелые заголовки C
const (
	// IP_BOUND_IF позволяет привязать сокет к конкретному интерфейсу (индекс)
	// Но чаще на iOS используется автоматическое исключение через NetworkExtension.
	// Если вы используете классический BSD сокет, можно пометить его:
	SO_NO_TC_NETPOLICY = 0x1101
)

func init() {
	tunnel.CustomProtectedDialer = DialContextWithProtect
	tunnel.CustomProtectedPacketDialer = DialUDPWithProtect
}

func DialContextWithProtect(ctx context.Context, network string, address string) (net.Conn, error) {
	d := &net.Dialer{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				// На iOS защита сокета часто происходит автоматически, если
				// адрес сервера исключен из маршрутов туннеля в Swift коде.
				// Однако, можно добавить системную опцию исключения:
				setNoNetworkPolicy(fd)
			})
		},
	}
	return d.DialContext(ctx, network, address)
}

func DialUDPWithProtect(ctx context.Context, network string, address string) (net.PacketConn, error) {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				setNoNetworkPolicy(fd)
			})
		},
	}

	pc, err := lc.ListenPacket(ctx, network, ":0")
	if err != nil {
		return nil, err
	}

	udpAddr, err := net.ResolveUDPAddr(network, address)
	if err != nil {
		pc.Close()
		return nil, err
	}

	log.Infof("[iOS] UDP Socket initialized for %s", address)

	return &connectedUDPConn{
		PacketConn: pc,
		remoteAddr: udpAddr,
	}, nil
}

func setNoNetworkPolicy(fd uintptr) {
	// На iOS это говорит системе "не применять политики туннелирования к этому сокету"
	syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, SO_NO_TC_NETPOLICY, 1)
}

// Повторяем структуру-обертку (или вынесите её в общий файл без тегов сборки)
type connectedUDPConn struct {
	net.PacketConn
	remoteAddr net.Addr
}

func (c *connectedUDPConn) Write(b []byte) (int, error) {
	return c.WriteTo(b, c.remoteAddr)
}

func (c *connectedUDPConn) RemoteAddr() net.Addr {
	return c.remoteAddr
}
