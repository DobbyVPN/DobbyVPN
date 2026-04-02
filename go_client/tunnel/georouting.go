package tunnel

import (
	"go_client/log"
	"net"
	"strings"
	"sync"
)

var (
	mu                 sync.Mutex
	DefaultBypassCIDRs []*net.IPNet
)

func mustCIDR(s string) {
	_, ipnet, err := net.ParseCIDR(s)
	if err != nil {
		addBypassHost(s)
	} else {
		DefaultBypassCIDRs = append(DefaultBypassCIDRs, ipnet)
	}
}

// SetGeoRoutingConf задаёт список подсетей, которые будут обходить VPN
func SetGeoRoutingConf(cidrs string) {
	mu.Lock()
	defer mu.Unlock()

	paths := strings.Fields(cidrs)

	for _, cidr := range paths {
		mustCIDR(cidr)
	}

	log.Infof("[Routing] Set DefaultBypassCIDRs: %v", DefaultBypassCIDRs)
}

// ClearGeoRoutingConf очищает список обхода маршрутизации
func ClearGeoRoutingConf() {
	mu.Lock()
	defer mu.Unlock()

	DefaultBypassCIDRs = nil
	log.Infof("[Routing] Cleared DefaultBypassCIDRs")
}

func resolveHostToCIDRs(host string) []*net.IPNet {
	ips, err := net.LookupIP(host)
	if err != nil {
		log.Infof("[Bypass] resolve failed for %s: %v", host, err)
		return nil
	}

	var result []*net.IPNet
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			_, n, _ := net.ParseCIDR(v4.String() + "/32")
			result = append(result, n)
			continue
		}
		_, n, _ := net.ParseCIDR(ip.String() + "/128")
		result = append(result, n)
	}
	return result
}

func addBypassHost(host string) {
	cidrs := resolveHostToCIDRs(host)
	if len(cidrs) == 0 {
		log.Infof("[Bypass] no IPs resolved for %s", host)
		return
	}

	routesMu.Lock()
	defer routesMu.Unlock()

	DefaultBypassCIDRs = append(DefaultBypassCIDRs, cidrs...)

	for _, c := range cidrs {
		log.Infof("[Bypass] added %s for host %s", c.String(), host)
	}
}
