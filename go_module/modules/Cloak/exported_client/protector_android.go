//go:build android
// +build android

package exported_client

import (
	"go_module/log"
	"go_module/tunnel/protected_dialer"
	"syscall"
)

func protector(network string, address string, c syscall.RawConn) error {
	return c.Control(func(fd uintptr) {
		if protected_dialer.MakeSocketProtected == nil {
			log.Infof("Protect skipped: socket protector is not registered for fd=%d %s %s", fd, network, address)
			return
		}
		protected_dialer.MakeSocketProtected(fd)
		log.Infof("Protect requested: fd=%d %s %s", fd, network, address)
	})
}
