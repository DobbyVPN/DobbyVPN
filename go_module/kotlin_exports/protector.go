//go:build android

package dobbyvpn

import (
	"go_module/log"
	"go_module/tunnel/protected_dialer"
	"sync"
)

type SocketProtector interface {
	Protect(fd int32) bool
}

var (
	socketProtector   SocketProtector
	socketProtectorMu sync.RWMutex
)

func init() {
	protected_dialer.MakeSocketProtected = protectSocket
}

func RegisterSocketProtector(protector SocketProtector) {
	socketProtectorMu.Lock()
	defer socketProtectorMu.Unlock()

	socketProtector = protector
	log.Debugf("kotlin_exports", "socket protector registered: %v", protector != nil)
}

func protectSocket(fd uintptr) bool {
	socketProtectorMu.RLock()
	protector := socketProtector
	socketProtectorMu.RUnlock()

	if protector == nil {
		log.Debugf("kotlin_exports", "socket protect skipped: protector is not registered")
		return false
	}
	if !protector.Protect(int32(fd)) {
		log.Debugf("kotlin_exports", "socket protect failed for fd=%d", fd)
		return false
	}
	log.Debugf("kotlin_exports", "socket protect succeeded for fd=%d", fd)
	return true
}
