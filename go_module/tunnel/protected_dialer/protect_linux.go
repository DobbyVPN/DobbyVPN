//go:build linux && !(android || ios)

package protected_dialer

import (
	"fmt"
	"math"
	"syscall"

	"go_module/log"
)

var linuxSocketMark int

func SetLinuxSocketMark(mark int) {
	linuxSocketMark = mark
	log.Infof("[Linux-Protect] SO_MARK=%d", mark)
}

type linuxProtector struct{}

func (l *linuxProtector) Protect(fdU uintptr, network string) {
	if linuxSocketMark == 0 {
		return
	}

	fd, err := UintptrToInt(fdU)
	if err != nil {
		log.Infof("[Linux-Protect] Protect fd err=%v", err)
	}

	_ = syscall.SetsockoptInt(
		fd,
		syscall.SOL_SOCKET,
		syscall.SO_MARK,
		linuxSocketMark,
	)
}

func init() {
	protector = &linuxProtector{}
}

func UintptrToInt(u uintptr) (int, error) {
	if u > uintptr(math.MaxInt) {
		return 0, fmt.Errorf("uintptr value %d overflows int", u)
	}
	return int(u), nil
}
