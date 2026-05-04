//go:build ios
// +build ios

package exported_client

import (
	"fmt"
	"syscall"

	"go_module/log"
)

const SO_NO_TC_NETPOLICY = 0x1101
const IP_BOUND_IF = 25

func protector(network string, address string, c syscall.RawConn) error {
	var protectErr error
	controlErr := c.Control(func(fd uintptr) {
		protectErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, SO_NO_TC_NETPOLICY, 1)
		if protectErr != nil {
			log.Infof("Cloak protect failed: fd=%d network=%s address=%s err=%v", fd, network, address, protectErr)
		} else {
			log.Infof("Cloak protect (SO_NO_TC_NETPOLICY) success: fd=%d network=%s address=%s", fd, network, address)
		}

		// iOS 26 research: Try IP_BOUND_IF as well
		boundIfErr := syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, IP_BOUND_IF, 0)
		if boundIfErr != nil {
			log.Infof("Cloak IP_BOUND_IF skipped: fd=%d err=%v", fd, boundIfErr)
		}
	})
	if controlErr != nil {
		return controlErr
	}
	if protectErr != nil {
		return fmt.Errorf("cloak iOS socket protect failed: %w", protectErr)
	}
	return nil
}
