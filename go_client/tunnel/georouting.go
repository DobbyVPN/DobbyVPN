package tunnel

import (
	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	"go_client/log"
	"net"
	"strings"
	"sync"
)

var (
	routesMu           sync.RWMutex
	DefaultBypassCIDRs []*net.IPNet
)

func IsBypass(metadata *M.Metadata) bool {
	if metadata == nil {
		return false
	}

	destIP := metadata.DstIP
	if !destIP.IsValid() {
		return false
	}

	routesMu.RLock()
	defer routesMu.RUnlock()

	stdIP := net.IP(destIP.AsSlice())

	for _, route := range DefaultBypassCIDRs {
		if route.Contains(stdIP) {
			log.Infof("[Router] BYPASS hit for IP: %s", stdIP)
			return true
		}
	}
	log.Infof("[Router] PROXY route for IP: %s", stdIP)
	return false
}

func mustCIDR(s string) {
	_, ipnet, err := net.ParseCIDR(s)
	if err != nil {
		addBypassHost(s)
	} else {
		DefaultBypassCIDRs = append(DefaultBypassCIDRs, ipnet)
	}
}

func SetGeoRoutingConf(cidrs string) {
	routesMu.Lock()
	defer routesMu.Unlock()

	paths := strings.Fields(cidrs)

	for _, cidr := range paths {
		mustCIDR(cidr)
	}

	log.Infof("[Routing] Set DefaultBypassCIDRs: %v", DefaultBypassCIDRs)
}

func ClearGeoRoutingConf() {
	routesMu.Lock()
	defer routesMu.Unlock()

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
