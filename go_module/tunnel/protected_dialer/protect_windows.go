//go:build windows && !(android || ios)

package protected_dialer

import (
	"syscall"

	"go_module/log"
)

var defaultInterfaceIndex int

func SetDefaultRoute(gatewayIP, interfaceName string, idx int) {
	defaultInterfaceIndex = idx
	log.Debugf(Category, "[Windows-Protect] default route gateway=%s iface=%s ifindex=%d", gatewayIP, interfaceName, idx)
}

// ResetDefaultRoute clears the generation-owned interface binding. It must be
// called when a session exits, including failed starts, so later protected
// sockets cannot inherit a stale physical interface.
func ResetDefaultRoute() {
	defaultInterfaceIndex = 0
}

type windowsProtector struct{}

func (w *windowsProtector) Protect(fd uintptr, network string) error {
	if defaultInterfaceIndex == 0 {
		return ErrSocketProtectionUnavailable
	}

	switch network {
	case "tcp4", "udp4":
		const IP_UNICAST_IF = 31
		idx := htonl(uint32(defaultInterfaceIndex))
		if err := syscall.SetsockoptInt(syscall.Handle(fd), syscall.IPPROTO_IP, IP_UNICAST_IF, int(idx)); err != nil {
			log.Debugf(Category, "[Windows-Protect] IP_UNICAST_IF failed fd=%d iface=%d network=%s err=%v", fd, defaultInterfaceIndex, network, err)
			return err
		}

	case "tcp6", "udp6":
		const IPV6_UNICAST_IF = 31
		if err := syscall.SetsockoptInt(syscall.Handle(fd), syscall.IPPROTO_IPV6, IPV6_UNICAST_IF, defaultInterfaceIndex); err != nil {
			log.Debugf(Category, "[Windows-Protect] IPV6_UNICAST_IF failed fd=%d iface=%d network=%s err=%v", fd, defaultInterfaceIndex, network, err)
			return err
		}
	}
	return nil
}

func htonl(i uint32) uint32 {
	return (i&0xff)<<24 | (i&0xff00)<<8 | (i&0xff0000)>>8 | (i&0xff000000)>>24
}

func init() {
	protector = &windowsProtector{}
}
