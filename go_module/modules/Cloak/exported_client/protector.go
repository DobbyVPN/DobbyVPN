//go:build !android
// +build !android

package exported_client

import (
	"syscall"

	"go_module/tunnel/protected_dialer"
)

func protector(network string, address string, c syscall.RawConn) error {
	return protected_dialer.ProtectRawConn(network, address, c)
}
