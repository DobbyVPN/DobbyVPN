package protected_dialer

import (
	"fmt"
	"net"
	"strings"
	"syscall"

	"go_module/log"
)

const SO_NO_TC_NETPOLICY = 0x1101

type iosProtector struct{}

func (i *iosProtector) Protect(fd uintptr, network string) {
	err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, SO_NO_TC_NETPOLICY, 1)
	if err != nil {
		log.Infof("[iOS-Protect] SO_NO_TC_NETPOLICY failed fd=%d network=%s err=%v interfaces=[%s]", fd, network, err, describeInterfacesForLog())
		return
	}
	log.Infof("[iOS-Protect] SO_NO_TC_NETPOLICY success fd=%d network=%s interfaces=[%s]", fd, network, describeInterfacesForLog())
}

func describeInterfacesForLog() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return fmt.Sprintf("scan_error=%v", err)
	}

	parts := make([]string, 0, len(interfaces))
	for _, iface := range interfaces {
		parts = append(parts, fmt.Sprintf("%s(index=%d flags=%s mtu=%d)", iface.Name, iface.Index, iface.Flags.String(), iface.MTU))
	}
	return strings.Join(parts, ";")
}

func init() {
	protector = &iosProtector{}
	log.Infof("[iOS-Protect] Initialized with SO_NO_TC_NETPOLICY diagnostics")
}
