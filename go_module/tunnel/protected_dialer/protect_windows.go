//go:build windows && !(android || ios)

package protected_dialer

import (
	"syscall"

	"go_module/log"
	"go_module/routing"

	"github.com/jackpal/gateway"
)

var defaultInterfaceIndex int
var defaultGatewayIP string
var defaultInterfaceName string

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
	log.Debugf(Category, "[Windows-Protect] ifindex=%d", idx)
}

func SetDefaultRoute(gatewayIP, interfaceName string, idx int) {
	defaultGatewayIP = gatewayIP
	defaultInterfaceName = interfaceName
	defaultInterfaceIndex = idx
	log.Debugf(Category, "[Windows-Protect] default route gateway=%s iface=%s ifindex=%d", gatewayIP, interfaceName, idx)
}

func GetDefaultRoute() (gatewayIP, interfaceName string, ok bool) {
	return defaultGatewayIP, defaultInterfaceName, defaultGatewayIP != "" && defaultInterfaceName != ""
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
		if err := syscall.SetsockoptInt(syscall.Handle(fd), syscall.IPPROTO_IP, IP_UNICAST_IF, int(idx)); err != nil {
			log.Debugf(Category, "[Windows-Protect] IP_UNICAST_IF failed fd=%d iface=%d network=%s err=%v", fd, defaultInterfaceIndex, network, err)
		}

	case "tcp6", "udp6":
		const IPV6_UNICAST_IF = 31
		if err := syscall.SetsockoptInt(syscall.Handle(fd), syscall.IPPROTO_IPV6, IPV6_UNICAST_IF, defaultInterfaceIndex); err != nil {
			log.Debugf(Category, "[Windows-Protect] IPV6_UNICAST_IF failed fd=%d iface=%d network=%s err=%v", fd, defaultInterfaceIndex, network, err)
		}
	}
}

func htonl(i uint32) uint32 {
	return (i&0xff)<<24 | (i&0xff00)<<8 | (i&0xff0000)>>8 | (i&0xff000000)>>24
}

func init() {
	protector = &windowsProtector{}
}
