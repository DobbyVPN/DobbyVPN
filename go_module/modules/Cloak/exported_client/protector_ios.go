//go:build ios
// +build ios

package exported_client

import (
	"fmt"
	"syscall"

	"go_module/log"
	"go_module/tunnel/protected_dialer"
)

const (
	SO_NO_TC_NETPOLICY = 0x1101
	IP_BOUND_IF        = 25
	IPV6_BOUND_IF      = 125
)

func protector(network string, address string, c syscall.RawConn) error {
	var protectErr error
	log.Infof("Cloak protect begin network=%s address=%s", network, address)
	controlErr := c.Control(func(fd uintptr) {
		log.Infof("Cloak protect control fd=%d network=%s address=%s", fd, network, address)
		// iOS 26+: Try SO_NO_TC_NETPOLICY (legacy, usually fails on iOS 26+)
		legacyErr := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, SO_NO_TC_NETPOLICY, 1)
		if legacyErr != nil {
			log.Infof("Cloak protect SO_NO_TC_NETPOLICY failed (expected on iOS 26+): fd=%d err=%v", fd, legacyErr)
		} else {
			log.Infof("Cloak protect SO_NO_TC_NETPOLICY success: fd=%d", fd)
		}

		// iOS 26+: Use IP_BOUND_IF with the actual interface index from Swift
		ifaceIndex := protected_dialer.GetDefaultInterfaceForIOS()
		if ifaceIndex > 0 {
			// Determine IP version from network type
			isIPv6 := network == "tcp6" || network == "udp6"

			if isIPv6 {
				if err := syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, IPV6_BOUND_IF, ifaceIndex); err != nil {
					log.Infof("Cloak IP_BOUND_IF (IPv6) failed: fd=%d iface=%d err=%v", fd, ifaceIndex, err)
					if legacyErr != nil {
						protectErr = err
					}
					return
				}
				log.Infof("Cloak IP_BOUND_IF (IPv6) success: fd=%d iface=%d", fd, ifaceIndex)
			} else {
				if err := syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, IP_BOUND_IF, ifaceIndex); err != nil {
					log.Infof("Cloak IP_BOUND_IF (IPv4) failed: fd=%d iface=%d err=%v", fd, ifaceIndex, err)
					if legacyErr != nil {
						protectErr = err
					}
					return
				}
				log.Infof("Cloak IP_BOUND_IF (IPv4) success: fd=%d iface=%d", fd, ifaceIndex)
			}

			log.Infof("Cloak protect success: fd=%d network=%s address=%s iface=%d", fd, network, address, ifaceIndex)
		} else {
			// No interface index available - log warning
			log.Infof("Cloak protect warning: no interface index available (ifaceIndex=%d), socket protection may fail on iOS 26+", ifaceIndex)
			if legacyErr != nil {
				protectErr = fmt.Errorf("no default interface index set for Cloak socket protection and SO_NO_TC_NETPOLICY failed: %w", legacyErr)
			}
		}
	})

	if controlErr != nil {
		log.Infof("Cloak protect raw control failed network=%s address=%s err=%v", network, address, controlErr)
		return controlErr
	}
	if protectErr != nil {
		log.Infof("Cloak protect finished with error network=%s address=%s err=%v", network, address, protectErr)
	} else {
		log.Infof("Cloak protect finished OK network=%s address=%s", network, address)
	}
	return protectErr
}
