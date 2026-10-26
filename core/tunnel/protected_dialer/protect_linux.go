//go:build linux && !(android || ios)

package protected_dialer

import (
	"fmt"
	"math"
	"sync/atomic"
	"syscall"

	"core/log"
)

var linuxSocketMark atomic.Int64

func SetLinuxSocketMark(mark int) {
	linuxSocketMark.Store(int64(mark))
	log.Debugf(Category, "[Linux-Protect] SO_MARK=%d", mark)
}

type linuxProtector struct{}

func (l *linuxProtector) Protect(fdU uintptr, network string) error {
	mark := int(linuxSocketMark.Load())
	if mark == 0 {
		return ErrSocketProtectionUnavailable
	}

	fd, err := UintptrToInt(fdU)
	if err != nil {
		log.Debugf(Category, "[Linux-Protect] Protect fd err=%v", err)
		return err
	}

	if err := syscall.SetsockoptInt(
		fd,
		syscall.SOL_SOCKET,
		syscall.SO_MARK,
		mark,
	); err != nil {
		log.Errorf(Category, "[Linux-Protect] SO_MARK failed fd=%d mark=%d err=%v", fd, mark, err)
		return err
	}
	return nil
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
