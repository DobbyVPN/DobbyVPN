package main

/*
// Объявляем прототип функции, которая будет в jni.c
extern int go_protect_socket(int fd);
*/
import "C"

import (
	"context"
	"go_client/log"
	"go_client/tunnel"
	"net"
	"syscall"
)

func init() {
	// Подключаем реализацию с C-хуком к логике роутера
	tunnel.CustomProtectedDialer = DialContextWithProtect
	tunnel.CustomProtectedPacketDialer = DialUDPWithProtect
}

func DialContextWithProtect(ctx context.Context, network string, address string) (net.Conn, error) {
	d := &net.Dialer{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				// Вызываем C-функцию, которая пробросит вызов в Kotlin
				C.go_protect_socket(C.int(fd))
			})
		},
	}
	return d.DialContext(ctx, network, address)
}

func DialUDPWithProtect(ctx context.Context, network string, address string) (net.PacketConn, error) {
	// 1. Создаем конфигурацию с защитой сокета
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				// Вызываем JNI protect ДО связывания сокета
				C.go_protect_socket(C.int(fd))
			})
		},
	}

	// 2. Начинаем слушать на случайном порту
	pc, err := lc.ListenPacket(ctx, network, ":0")
	if err != nil {
		return nil, err
	}

	// 3. Чтобы PacketConn вел себя как "подключенный" сокет (Dial),
	// нам нужно преобразовать адрес в net.Addr
	udpAddr, err := net.ResolveUDPAddr(network, address)
	if err != nil {
		pc.Close()
		return nil, err
	}

	// Если твоя логика в tunnel.go ожидает, что PacketConn уже "смотрит" на сервер,
	// мы можем использовать тип-обертку или просто возвращать pc.
	// Но важно понимать: в UDP protect() работает на дескриптор,
	// и теперь любой пакет через pc пойдет мимо VPN.

	log.Infof("[Go] UDP Socket protected and bound for %s", address)

	return &connectedUDPConn{
		PacketConn: pc,
		remoteAddr: udpAddr,
	}, nil
}

// Вспомогательная структура, чтобы имитировать поведение DialUDP
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
