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
	log.Infof("socket protector registered: %v", protector != nil)
}

func protectSocket(fd uintptr) bool {
	socketProtectorMu.RLock()
	protector := socketProtector
	socketProtectorMu.RUnlock()

	if protector == nil {
		log.Infof("socket protect skipped: protector is not registered")
		return false
	}
	if !protector.Protect(int32(fd)) {
		log.Infof("socket protect failed for fd=%d", fd)
		return false
	}
	log.Infof("socket protect succeeded for fd=%d", fd)
	return true
}
