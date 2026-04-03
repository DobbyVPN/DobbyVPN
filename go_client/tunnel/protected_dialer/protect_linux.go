//go:build linux && !(android || ios)

package protected_dialer

import (
	"syscall"

	"go_client/log"
)

var linuxSocketMark int

func SetLinuxSocketMark(mark int) {
	linuxSocketMark = mark
	log.Infof("[Linux-Protect] SO_MARK=%d", mark)
}

type linuxProtector struct{}

func (l *linuxProtector) Protect(fd uintptr, network string) {
	if linuxSocketMark == 0 {
		return
	}

	_ = syscall.SetsockoptInt(
		int(fd),
		syscall.SOL_SOCKET,
		syscall.SO_MARK,
		linuxSocketMark,
	)
}

func init() {
	protector = &linuxProtector{}
}
