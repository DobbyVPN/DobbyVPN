//go:build windows && !(android || ios)

package protected_dialer

import (
	"fmt"
	"syscall"

	"core/log"
)

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
	selectedIndex, err := physicalInterfaceIndex(network)
	if err != nil {
		return err
	}

	level, option, ipv4, err := windowsInterfaceOption(network)
	if err != nil {
		return err
	}
	optionIndex := selectedIndex
	if ipv4 {
		idx := htonl(uint32(selectedIndex))
		optionIndex = int(idx)
	}
	if err := syscall.SetsockoptInt(syscall.Handle(fd), level, option, optionIndex); err != nil {
		optionName := "IPV6_UNICAST_IF"
		if ipv4 {
			optionName = "IP_UNICAST_IF"
		}
		log.Errorf(Category, "[Windows-Protect] %s failed fd=%d iface=%d network=%s err=%v", optionName, fd, selectedIndex, network, err)
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
