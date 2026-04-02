//go:build darwin && !(android || ios)

package protected_dialer

import "syscall"

const (
	IP_BOUND_IF   = 25
	IPV6_BOUND_IF = 125
)

var defaultInterfaceIndex int

func SetDefaultInterface(idx int) {
	defaultInterfaceIndex = idx
}

type macosProtector struct{}

func (m *macosProtector) Protect(fd uintptr, network string) {
	if defaultInterfaceIndex == 0 {
		return
	}

	switch network {
	case "tcp4", "udp4":
		_ = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, IP_BOUND_IF, defaultInterfaceIndex)
	case "tcp6", "udp6":
		_ = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, IPV6_BOUND_IF, defaultInterfaceIndex)
	}
}

func init() {
	protector = &macosProtector{}
}
