package tunnel

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"

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
			log.Debugf(Category, "[Router] BYPASS hit for IP: %s", stdIP)
			return true
		}
	}
	log.Debugf(Category, "[Router] PROXY route for IP: %s", stdIP)
	return false
}

func SetGeoRoutingConf(cidrs string) {
	paths := strings.Fields(cidrs)
	resolvedRoutes := make([]*net.IPNet, 0, len(paths))

	for _, entry := range paths {
		resolvedRoutes = append(resolvedRoutes, bypassCIDRsForEntry(entry)...)
	}

	routesMu.Lock()
	defer routesMu.Unlock()

	defaultBypassCIDRs = resolvedRoutes

	log.Debugf(Category, "[Routing] Set defaultBypassCIDRs: %v", defaultBypassCIDRs)
}

func ClearGeoRoutingConf() {
	routesMu.Lock()
	defer routesMu.Unlock()

	defaultBypassCIDRs = nil
	log.Debugf(Category, "[Routing] Cleared defaultBypassCIDRs")
}

func bypassCIDRsForEntry(entry string) []*net.IPNet {
	_, ipnet, err := net.ParseCIDR(entry)
	if err == nil {
		log.Debugf(Category, "[Bypass] added %s", ipnet.String())
		return []*net.IPNet{ipnet}
	}

	cidrs := resolveHostToCIDRs(entry)
	if len(cidrs) == 0 {
		log.Debugf(Category, "[Bypass] no IPs resolved for %s", entry)
		return nil
	}
	for _, c := range cidrs {
		log.Debugf(Category, "[Bypass] added %s for host %s", c.String(), entry)
	}
	return cidrs
}

func resolveHostToCIDRs(host string) []*net.IPNet {
	resolver := net.Resolver{}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		log.Debugf(Category, "[Bypass] resolve failed for %s elapsedMs=%d err=%v", host, time.Since(start).Milliseconds(), err)
		return nil
	}
	log.Debugf(Category, "[Bypass] resolve OK host=%s count=%d elapsedMs=%d", host, len(ips), time.Since(start).Milliseconds())

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
