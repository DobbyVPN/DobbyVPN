//go:build darwin && !(android || ios)

package protected_dialer

import (
	"fmt"
	"net"
	"strings"
	"syscall"

	"go_module/log"
)

const (
	ipBoundIf   = 25
	ipv6BoundIf = 125
)

var defaultInterfaceIndex int

func isReachableViaInterface(iface net.Interface, gw net.IP) bool {
	addrs, _ := iface.Addrs()

	for _, addr := range addrs {
		ip, ipnet, _ := net.ParseCIDR(addr.String())
		if ip == nil || ip.To4() == nil {
			continue
		}

		if ipnet.Contains(gw) {
			log.Debugf(Category, "[Darwin-Protect][Detect] iface=%s contains gateway %s (cidr=%s)", iface.Name, gw.String(), ipnet.String())
			return true
		}
	}

	return false
}

func GetDefaultInterfaceNameDarwin(gatewayIP net.IP) (name string, index int, err error) {
	log.Debugf(Category, "[Darwin-Protect][Detect] Gateway detected: %s", gatewayIP.String())

	ifaces, err := net.Interfaces()
	if err != nil {
		return "", 0, err
	}

	for _, iface := range ifaces {

		log.Debugf(Category, "[Darwin-Protect][Detect] Checking iface=%s flags=%v", iface.Name, iface.Flags)

		if iface.Flags&net.FlagUp == 0 {
			log.Debugf(Category, "[Darwin-Protect][Detect] skip %s: down", iface.Name)
			continue
		}
		if iface.Flags&net.FlagLoopback != 0 {
			log.Debugf(Category, "[Darwin-Protect][Detect] skip %s: loopback", iface.Name)
			continue
		}
		if len(iface.HardwareAddr) == 0 {
			log.Debugf(Category, "[Darwin-Protect][Detect] skip %s: no MAC", iface.Name)
			continue
		}

		if strings.HasPrefix(iface.Name, "utun") ||
			strings.HasPrefix(iface.Name, "awdl") ||
			strings.HasPrefix(iface.Name, "llw") ||
			strings.HasPrefix(iface.Name, "bridge") ||
			strings.HasPrefix(iface.Name, "lo") {
			log.Debugf(Category, "[Darwin-Protect][Detect] skip %s: virtual/unsupported", iface.Name)
			continue
		}

		if isReachableViaInterface(iface, gatewayIP) {
			log.Debugf(Category, "[Darwin-Protect][Detect] SELECTED iface=%s index=%d (gateway reachable)", iface.Name, iface.Index)
			return iface.Name, iface.Index, nil
		}
	}

	return "", 0, fmt.Errorf("no interface for gateway found")
}

func SetDefaultInterface(idx int) {
	defaultInterfaceIndex = idx
}

type macosProtector struct{}

func (m *macosProtector) Protect(fd uintptr, network string) {
	if defaultInterfaceIndex == 0 {
		return
	}

	switch network {
	case networkTCP4, networkUDP4:
		if err := syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, ipBoundIf, defaultInterfaceIndex); err != nil {
			log.Debugf(Category, "[Darwin-Protect] IP_BOUND_IF failed fd=%d iface=%d network=%s err=%v", fd, defaultInterfaceIndex, network, err)
		}
	case networkTCP6, networkUDP6:
		if err := syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, ipv6BoundIf, defaultInterfaceIndex); err != nil {
			log.Debugf(Category, "[Darwin-Protect] IPV6_BOUND_IF failed fd=%d iface=%d network=%s err=%v", fd, defaultInterfaceIndex, network, err)
		}
	}
}

func init() {
	protector = &macosProtector{}
}
