package georouting

import (
	"encoding/binary"
	"errors"
	"net"
)

// RouteAction описывает, что делать с пакетом.
type RouteAction int

const (
	RouteProxy  RouteAction = iota // через VPN
	RouteDirect                    // обход VPN
)

// DecideOutbound решает, куда отправлять пакет,
// который пришёл из ОС (читаем из TUN, собираемся отдать в VPN или direct).
func DecideOutbound(ipPacket []byte) RouteAction {
	ip, err := extractDstIP(ipPacket)
	if err != nil {
		// На всякий случай если что-то не так — ведём себя как раньше
		return RouteProxy
	}
	if inBypass(ip) {
		return RouteDirect
	}
	return RouteProxy
}

// DecideInbound – на будущее, если тебе понадобится другая логика для входящих пакетов.
// Сейчас просто прокидывает всё через VPN.
func DecideInbound(ipPacket []byte) RouteAction {
	return RouteProxy
}

// ----- внутренние хелперы -----

// extractDstIP поддерживает пока только IPv4.
// При желании можно добавить IPv6 по версии из заголовка.
func extractDstIP(pkt []byte) (net.IP, error) {
	if len(pkt) < 20 {
		return nil, errors.New("packet too short for IPv4 header")
	}

	version := pkt[0] >> 4
	if version != 4 {
		// IPv6 и прочее — пока не трогаем
		return nil, errors.New("unsupported IP version")
	}

	// В IPv4 dst-IP лежит в байтах 16..19
	dst := net.IPv4(pkt[16], pkt[17], pkt[18], pkt[19])
	return dst, nil
}

func inBypass(ip net.IP) bool {
	if len(DefaultBypassCIDRs) == 0 {
		return false
	}
	for _, n := range DefaultBypassCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// На будущее, если захочешь быстрый lookup по uint32
func ip4ToUint32(ip net.IP) uint32 {
	ip4 := ip.To4()
	if ip4 == nil {
		return 0
	}
	return binary.BigEndian.Uint32(ip4)
}
