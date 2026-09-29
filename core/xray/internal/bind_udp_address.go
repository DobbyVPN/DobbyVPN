//go:build linux || android

package internal

import (
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

func bindPlatformUDPAddress(fd uintptr, address []byte, port uint32) error {
	// Match Xray's Linux bindAddr behavior. These options are best-effort, as
	// they are in the pinned default dialer.
	_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
	_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, unix.SO_REUSEPORT, 1)
	var sockaddr syscall.Sockaddr
	switch len(address) {
	case 4:
		a4 := &syscall.SockaddrInet4{Port: int(port)}
		copy(a4.Addr[:], address)
		sockaddr = a4
	case 16:
		a6 := &syscall.SockaddrInet6{Port: int(port)}
		copy(a6.Addr[:], address)
		sockaddr = a6
	default:
		return fmt.Errorf("unexpected bind address length %d", len(address))
	}
	return syscall.Bind(int(fd), sockaddr)
}
