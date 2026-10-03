//go:build darwin && !(android || ios)

package platform_engine

import (
	"errors"
	"fmt"
	"net/netip"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Darwin's in_aliasreq and in6_aliasreq layouts, from the public XNU headers.
// Only the newly created utun is configured; closing it releases these addresses.
type macOSIPv4Alias struct {
	Name                       [unix.IFNAMSIZ]byte
	Address, Destination, Mask unix.RawSockaddrInet4
}
type macOSIPv6Alias struct {
	Name                             [unix.IFNAMSIZ]byte
	Address, Destination, Mask       unix.RawSockaddrInet6
	Flags                            int32
	Expires, Preferred               int64
	ValidLifetime, PreferredLifetime uint32
}
type macOSInterfaceFlags struct {
	Name    [unix.IFNAMSIZ]byte
	Flags   uint16
	Padding [14]byte
}

func macOSIoctl(fd int, request uintptr, value unsafe.Pointer) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), request, uintptr(value))
	if errno != 0 {
		return errno
	}
	return nil
}

func configureMacOSInterface(name string) error {
	if len(name) >= unix.IFNAMSIZ {
		return fmt.Errorf("invalid utun name %q", name)
	}
	if err := configureMacOSIPv4(name); err != nil {
		return err
	}
	return configureMacOSIPv6(name)
}

func configureMacOSIPv4(name string) (resultErr error) {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, unix.Close(fd)) }()
	address := func(ip string) unix.RawSockaddrInet4 {
		return unix.RawSockaddrInet4{Len: unix.SizeofSockaddrInet4, Family: unix.AF_INET, Addr: netip.MustParseAddr(ip).As4()}
	}
	alias := macOSIPv4Alias{Address: address("198.18.0.1"), Destination: address("198.18.0.2"), Mask: address("255.255.0.0")}
	copy(alias.Name[:], name)
	if err := macOSIoctl(fd, unix.SIOCAIFADDR, unsafe.Pointer(&alias)); err != nil {
		return fmt.Errorf("configure %s IPv4: %w", name, err)
	}
	flags := macOSInterfaceFlags{}
	copy(flags.Name[:], name)
	if err := macOSIoctl(fd, unix.SIOCGIFFLAGS, unsafe.Pointer(&flags)); err != nil {
		return err
	}
	flags.Flags |= unix.IFF_UP
	return macOSIoctl(fd, unix.SIOCSIFFLAGS, unsafe.Pointer(&flags))
}

func configureMacOSIPv6(name string) (resultErr error) {
	fd, err := unix.Socket(unix.AF_INET6, unix.SOCK_DGRAM, 0)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, unix.Close(fd)) }()
	address := func(ip string) unix.RawSockaddrInet6 {
		return unix.RawSockaddrInet6{Len: unix.SizeofSockaddrInet6, Family: unix.AF_INET6, Addr: netip.MustParseAddr(ip).As16()}
	}
	alias := macOSIPv6Alias{Address: address("fd00:dbb::2"), Destination: address("fd00:dbb::1"), Mask: address("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"), Flags: 0x20, ValidLifetime: 0xffffffff, PreferredLifetime: 0xffffffff}
	copy(alias.Name[:], name)
	// _IOW('i',26,struct in6_aliasreq), with 64-bit time_t on supported Macs.
	request := uintptr(0x80000000 | (unsafe.Sizeof(alias) << 16) | ('i' << 8) | 26)
	if err := macOSIoctl(fd, request, unsafe.Pointer(&alias)); err != nil {
		return fmt.Errorf("configure %s IPv6: %w", name, err)
	}
	return nil
}
