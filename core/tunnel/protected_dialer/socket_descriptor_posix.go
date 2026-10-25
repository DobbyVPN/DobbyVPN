//go:build android || darwin || ios || linux

package protected_dialer

import (
	"fmt"
	"syscall"
)

func socketNetworkFromDescriptor(fd int) (string, error) {
	address, err := syscall.Getsockname(fd)
	if err != nil {
		return "", fmt.Errorf("getsockname: %w", err)
	}

	var family int
	switch address.(type) {
	case *syscall.SockaddrInet4:
		family = syscall.AF_INET
	case *syscall.SockaddrInet6:
		family = syscall.AF_INET6
	default:
		return "", fmt.Errorf("unsupported socket address family %T", address)
	}

	socketType, err := syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_TYPE)
	if err != nil {
		return "", fmt.Errorf("getsockopt SO_TYPE: %w", err)
	}
	return socketNetwork(family, socketType)
}
