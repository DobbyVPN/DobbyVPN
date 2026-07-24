//go:build android

package protected_dialer

import (
	"fmt"

	"go_module/log"
)

var MakeSocketProtected func(fd uintptr) bool

type androidProtector struct{}

func ProtectSocket(fd uintptr, network string) bool {
	if MakeSocketProtected == nil {
		log.Debugf(Category, "[Android-Protect] skipped: socket protector is not registered fd=%d network=%s", fd, network)
		return false
	}
	if !MakeSocketProtected(fd) {
		log.Debugf(Category, "[Android-Protect] failed fd=%d network=%s", fd, network)
		return false
	}
	log.Debugf(Category, "[Android-Protect] succeeded fd=%d network=%s", fd, network)
	return true
}

func (a *androidProtector) Protect(fd uintptr, network string) error {
	if !ProtectSocket(fd, network) {
		return fmt.Errorf("%w: Android VpnService.protect rejected fd=%d", ErrSocketProtectionUnavailable, fd)
	}
	return nil
}

func init() {
	protector = &androidProtector{}
}
