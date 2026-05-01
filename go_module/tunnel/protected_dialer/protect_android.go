//go:build android

package protected_dialer

import "fmt"

var MakeSocketProtected func(fd uintptr) bool

type androidProtector struct{}

func (a *androidProtector) Protect(fd uintptr, network string) error {
	if MakeSocketProtected == nil {
		return fmt.Errorf("android socket protector is not registered")
	}
	if !MakeSocketProtected(fd) {
		return fmt.Errorf("android VpnService.protect(%d) returned false", fd)
	}
	return nil
}

func init() {
	protector = &androidProtector{}
}
