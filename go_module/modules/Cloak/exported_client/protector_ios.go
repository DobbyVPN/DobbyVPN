//go:build ios
// +build ios

package exported_client

import (
	"fmt"
	"syscall"

	"go_module/log"
)

const SO_NO_TC_NETPOLICY = 0x1101

func protector(network string, address string, c syscall.RawConn) error {
	var protectErr error
	controlErr := c.Control(func(fd uintptr) {
		protectErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, SO_NO_TC_NETPOLICY, 1)
		if protectErr != nil {
			log.Infof("Cloak protect failed: fd=%d network=%s address=%s err=%v", fd, network, address, protectErr)
			return
		}
		log.Infof("Cloak protect success: fd=%d network=%s address=%s", fd, network, address)
	})
	if controlErr != nil {
		return controlErr
	}
	if protectErr != nil {
		return fmt.Errorf("cloak iOS socket protect failed: %w", protectErr)
	}
	return nil
}
