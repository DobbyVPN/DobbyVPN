//go:build darwin && !(android || ios)

package protected_dialer

import (
	"fmt"
	"syscall"

	"core/log"
)

const (
	ipBoundIf   = 25
	ipv6BoundIf = 125
)

type macosProtector struct{}

func macOSInterfaceOption(network string) (level, option int, err error) {
	switch network {
	case networkTCP4, networkUDP4:
		return syscall.IPPROTO_IP, ipBoundIf, nil
	case networkTCP6, networkUDP6:
		return syscall.IPPROTO_IPV6, ipv6BoundIf, nil
	default:
		return 0, 0, fmt.Errorf("unsupported protected network %q", network)
	}
}

func (m *macosProtector) Protect(fd uintptr, network string) error {
	interfaceIndex, err := physicalInterfaceIndex(network)
	if err != nil {
		return err
	}

	level, option, err := macOSInterfaceOption(network)
	if err != nil {
		return err
	}
	if err := syscall.SetsockoptInt(int(fd), level, option, interfaceIndex); err != nil {
		optionName := "IPV6_BOUND_IF"
		if level == syscall.IPPROTO_IP {
			optionName = "IP_BOUND_IF"
		}
		log.Errorf(Category, "[Darwin-Protect] %s failed fd=%d iface=%d network=%s err=%v", optionName, fd, interfaceIndex, network, err)
		return err
	}
	return nil
}

func init() {
	protector = &macosProtector{}
}
