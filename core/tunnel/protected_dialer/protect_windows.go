//go:build windows && !(android || ios)

package protected_dialer

import (
	"core/routing"
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

func (w *windowsProtector) Protect(fd uintptr, network, address string) error {
	return w.ProtectDestinations(fd, network, []string{address})
}

func (w *windowsProtector) ProtectDestinations(fd uintptr, network string, addresses []string) error {
	level, option, ipv4, err := windowsInterfaceOption(network)
	if err != nil {
		return err
	}
	boundIndex, err := syscall.GetsockoptInt(syscall.Handle(fd), level, option)
	if err != nil {
		return fmt.Errorf("read existing underlay socket binding: %w", err)
	}
	var preferred uint64
	if boundIndex > 0 {
		if identity, identityErr := routing.WindowsUnderlayIdentity(uint32(boundIndex)); identityErr == nil {
			preferred = identity.LUID
		} else {
			log.Debugf(Category, "[Windows-Protect] ignoring stale socket binding index=%d err=%v", boundIndex, identityErr)
		}
	}
	selected, err := selectCompatibleUnderlays(network, addresses, preferred, routing.WindowsUnderlayForDestination)
	if err != nil {
		return err
	}
	if err := routing.ValidateWindowsUnderlay(selected); err != nil {
		return err
	}
	optionIndex := selected.Index
	if ipv4 {
		optionIndex = htonl(optionIndex)
	}
	if err := syscall.SetsockoptInt(syscall.Handle(fd), level, option, int(optionIndex)); err != nil {
		log.Errorf(Category, "[Windows-Protect] bind failed fd=%d iface=%d network=%s err=%v", fd, selected.Index, network, err)
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
