//go:build windows && !(android || ios)

package protected_dialer

import (
	"syscall"

	"github.com/jackpal/gateway"
	"go_module/log"
	"go_module/routing"
)

var defaultInterfaceIndex int

func GetDefaultInterfaceIndex() (int, error) {
	gw, err := gateway.DiscoverGateway()
	if err != nil {
		return 0, err
	}

	ip, err := routing.FindInterfaceIPByGateway(gw.String())
	if err != nil {
		return 0, err
	}

	iface, err := routing.GetNetworkInterfaceByIP(ip)
	if err != nil {
		return 0, err
	}

	return iface.Index, nil
}

func SetDefaultInterfaceIndex(idx int) {
	defaultInterfaceIndex = idx
	log.Infof("[Windows-Protect] ifindex=%d", idx)
}

type windowsProtector struct{}

func (w *windowsProtector) Protect(fd uintptr, network string) {
	if defaultInterfaceIndex == 0 {
		return
	}

	switch network {
	case "tcp4", "udp4":
		const IP_UNICAST_IF = 31
		idx := htonl(uint32(defaultInterfaceIndex))
		_ = syscall.SetsockoptInt(syscall.Handle(fd), syscall.IPPROTO_IP, IP_UNICAST_IF, int(idx))

	case "tcp6", "udp6":
		const IPV6_UNICAST_IF = 31
		_ = syscall.SetsockoptInt(syscall.Handle(fd), syscall.IPPROTO_IPV6, IPV6_UNICAST_IF, defaultInterfaceIndex)
	}
}

func htonl(i uint32) uint32 {
	return (i&0xff)<<24 | (i&0xff00)<<8 | (i&0xff0000)>>8 | (i&0xff000000)>>24
}

func init() {
	protector = &windowsProtector{}
}
