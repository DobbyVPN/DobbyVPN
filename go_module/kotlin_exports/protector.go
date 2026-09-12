//go:build android

package dobbyvpn

import (
	"go_module/tunnel/protected_dialer"
)

func init() {
	protected_dialer.MakeSocketProtected = protectSocket
}

func protectSocket(fd uintptr) bool {
	if mobileSessions.ProtectActiveSocket(int32(fd)) {
		return true
	}
	return false
}
