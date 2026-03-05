package main

/*
// Объявляем прототип функции, которая будет в jni.c
extern int go_protect_socket(int fd);
*/
import "C"

import (
	"context"
	"go_client/tunnel"
	"net"
	"syscall"
)

func init() {
	// Подключаем реализацию с C-хуком к логике роутера
	tunnel.CustomProtectedDialer = DialContextWithProtect
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
