//go:build android
// +build android

package exported_client

import (
	"go_module/tunnel/protected_dialer"
	"syscall"
)

func protector(network string, address string, c syscall.RawConn) error {
	return c.Control(func(fd uintptr) {
		protected_dialer.ProtectSocket(fd, network)
	})
}
