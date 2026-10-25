//go:build windows && !(android || ios)

package protected_dialer

import (
	"fmt"
	"syscall"

	"core/log"
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

func windowsInterfaceOption(network string) (level, option int, ipv4 bool, err error) {
	switch network {
	case networkTCP4, networkUDP4:
		return syscall.IPPROTO_IP, 31, true, nil
	case networkTCP6, networkUDP6:
		return syscall.IPPROTO_IPV6, 31, false, nil
	default:
		return 0, 0, false, fmt.Errorf("unsupported protected network %q", network)
	}
}

func (w *windowsProtector) Protect(fd uintptr, network string) error {
	if defaultInterfaceIndex == 0 {
		return ErrSocketProtectionUnavailable
	}

	level, option, ipv4, err := windowsInterfaceOption(network)
	if err != nil {
		return err
	}
	interfaceIndex := defaultInterfaceIndex
	if ipv4 {
		idx := htonl(uint32(defaultInterfaceIndex))
		interfaceIndex = int(idx)
	}
	if err := syscall.SetsockoptInt(syscall.Handle(fd), level, option, interfaceIndex); err != nil {
		optionName := "IPV6_UNICAST_IF"
		if ipv4 {
			optionName = "IP_UNICAST_IF"
		}
		log.Errorf(Category, "[Windows-Protect] %s failed fd=%d iface=%d network=%s err=%v", optionName, fd, defaultInterfaceIndex, network, err)
		return err
	}
	return nil
}

func htonl(i uint32) uint32 {
	return (i&0xff)<<24 | (i&0xff00)<<8 | (i&0xff0000)>>8 | (i&0xff000000)>>24
}

func init() {
	protector = &windowsProtector{}
}
