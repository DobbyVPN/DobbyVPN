package tunnel

import (
	"context"
	"net"
	"strings"
	"sync"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"

	"go_module/log"
)

var (
	routesMu           sync.RWMutex
	defaultBypassCIDRs []*net.IPNet
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

	for _, route := range defaultBypassCIDRs {
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
		defaultBypassCIDRs = append(defaultBypassCIDRs, ipnet)
	}
}

func SetGeoRoutingConf(cidrs string) {
	routesMu.Lock()
	defer routesMu.Unlock()

	paths := strings.Fields(cidrs)

	for _, cidr := range paths {
		mustCIDR(cidr)
	}

	log.Infof("[Routing] Set defaultBypassCIDRs: %v", defaultBypassCIDRs)
}

func ClearGeoRoutingConf() {
	routesMu.Lock()
	defer routesMu.Unlock()

	defaultBypassCIDRs = nil
	log.Infof("[Routing] Cleared defaultBypassCIDRs")
}

func resolveHostToCIDRs(host string) []*net.IPNet {
	resolver := net.Resolver{}

	ctx := context.Background()
	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		log.Infof("[Bypass] resolve failed for %s: %v", host, err)
		return nil
	}

	var result []*net.IPNet
	for _, ip := range ips {
		if v4 := ip.IP.To4(); v4 != nil {
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

	defaultBypassCIDRs = append(defaultBypassCIDRs, cidrs...)

	for _, c := range cidrs {
		log.Infof("[Bypass] added %s for host %s", c.String(), host)
	}
}
