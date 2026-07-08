//go:build linux && !(android || ios)

package protected_dialer

import (
	"fmt"
	"math"
	"syscall"

	"go_module/log"
)

var linuxSocketMark int
var defaultGatewayIP string
var defaultInterfaceName string

func SetLinuxSocketMark(mark int) {
	linuxSocketMark = mark
	log.Debugf(Category, "[Linux-Protect] SO_MARK=%d", mark)
}

func SetDefaultRoute(gatewayIP, interfaceName string, _ int) {
	defaultGatewayIP = gatewayIP
	defaultInterfaceName = interfaceName
	log.Debugf(Category, "[Linux-Protect] default route gateway=%s iface=%s", gatewayIP, interfaceName)
}

func GetDefaultRoute() (gatewayIP, interfaceName string, ok bool) {
	return defaultGatewayIP, defaultInterfaceName, defaultGatewayIP != "" && defaultInterfaceName != ""
}

type linuxProtector struct{}

func (l *linuxProtector) Protect(fdU uintptr, network string) {
	if linuxSocketMark == 0 {
		return
	}

	fd, err := UintptrToInt(fdU)
	if err != nil {
		log.Debugf(Category, "[Linux-Protect] Protect fd err=%v", err)
	}

	if err := syscall.SetsockoptInt(
		fd,
		syscall.SOL_SOCKET,
		syscall.SO_MARK,
		linuxSocketMark,
	); err != nil {
		log.Debugf(Category, "[Linux-Protect] SO_MARK failed fd=%d mark=%d err=%v", fd, linuxSocketMark, err)
	}
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
