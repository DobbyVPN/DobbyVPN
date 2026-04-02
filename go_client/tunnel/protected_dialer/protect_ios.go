package protected_dialer

import "syscall"

const SO_NO_TC_NETPOLICY = 0x1101

type iosProtector struct{}

func (i *iosProtector) Protect(fd uintptr, network string) {
	_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, SO_NO_TC_NETPOLICY, 1)
}

func init() {
	protector = &iosProtector{}
}
