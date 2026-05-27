//go:build android

package protected_dialer

import "go_module/log"

var MakeSocketProtected func(fd uintptr) bool

type androidProtector struct{}

func ProtectSocket(fd uintptr, network string) bool {
	if MakeSocketProtected == nil {
		log.Infof("[Android-Protect] skipped: socket protector is not registered fd=%d network=%s", fd, network)
		return false
	}
	if !MakeSocketProtected(fd) {
		log.Infof("[Android-Protect] failed fd=%d network=%s", fd, network)
		return false
	}
	log.Infof("[Android-Protect] succeeded fd=%d network=%s", fd, network)
	return true
}

func (a *androidProtector) Protect(fd uintptr, network string) {
	ProtectSocket(fd, network)
}

func init() {
	protector = &androidProtector{}
}
