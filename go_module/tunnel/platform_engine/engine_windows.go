//go:build windows && !(android || ios)

package platform_engine

import (
	"fmt"
	"go_module/common"
	"net"
	"strings"
	"time"

	"go_module/log"
	"go_module/routing"

	"github.com/xjasonlyu/tun2socks/v2/engine"
)

var (
	lastIface string
	prevDNS   []string
	prevDHCP  bool
)

func execAndLog(cmd string, context string) error {
	startedAt := time.Now()
	out, err := routing.ExecuteCommand(cmd)
	elapsed := time.Since(startedAt).Truncate(time.Millisecond)
	if err != nil {
		log.Debugf(Category, "[Engine][Windows][ERROR] %s elapsed=%s: %v | output=%s",
			context, elapsed, err, out,
		)
		return err
	}

	log.Debugf(Category, "[Engine][Windows][OK] %s elapsed=%s: %s", context, elapsed, out)
	return nil
}

func startPlatformEngine(cfg interface{}) error {
	startedAt := time.Now()
	c := cfg.(EngineConfig)
	proxyAddr := c.ProxyAddr
	uplinkIface := c.UplinkIface

	log.Debugf(Category, "[Engine][Windows] proxy=%s iface=%s", proxyAddr, uplinkIface)
	if routing.IsTunnelInterfaceName(uplinkIface) {
		return fmt.Errorf("refusing to use tunnel interface %q as Windows uplink", uplinkIface)
	}

	key := &engine.Key{
		Proxy:     fmt.Sprintf("socks5://%s", proxyAddr),
		Device:    "wintun",
		Interface: uplinkIface,
		LogLevel:  "info",
		MTU:       1200,
	}

	engine.Insert(key)
	engineStartAt := time.Now()
	engine.Start()
	log.Debugf(Category, "[Engine][Windows] engine.Start returned elapsed=%s total=%s", time.Since(engineStartAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	waitStartedAt := time.Now()
	ifName, err := waitForWintun(5 * time.Second)
	if err != nil {
		engine.Stop()
		return err
	}
	log.Debugf(Category, "[Engine][Windows] waitForWintun OK iface=%s elapsed=%s total=%s", ifName, time.Since(waitStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	lastIface = ifName

	dnsReadStartedAt := time.Now()
	prevDNS, prevDHCP = getCurrentDNS(ifName)
	log.Debugf(Category, "[Engine][Windows] getCurrentDNS elapsed=%s total=%s", time.Since(dnsReadStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	tunCfg := common.GetNetworkConfig()

	if err := setInterfaceAddress(ifName, tunCfg.TunDevice); err != nil {
		engine.Stop()
		return err
	}
	if err := setDNS(ifName, "1.1.1.1"); err != nil {
		engine.Stop()
		return err
	}

	log.Debugf(Category, "[Engine][Windows] platform engine ready iface=%s elapsed=%s", ifName, time.Since(startedAt).Truncate(time.Millisecond))
	return nil
}

func stopPlatformEngine() {
	if lastIface == "" {
		return
	}

	log.Debugf(Category, "[Engine][Windows] Restoring DNS. DHCP=%v DNS=%v", prevDHCP, prevDNS)

	if prevDHCP {
		cmd := fmt.Sprintf(
			"netsh interface ipv4 set dnsservers name=\"%s\" dhcp",
			lastIface,
		)
		_ = execAndLog(cmd, "restore DNS (DHCP)")

	} else if len(prevDNS) > 0 {

		// primary
		cmd := fmt.Sprintf(
			"netsh interface ipv4 set dnsservers name=\"%s\" static %s primary",
			lastIface, prevDNS[0],
		)
		_ = execAndLog(cmd, "restore DNS primary")

		// additional
		for i := 1; i < len(prevDNS); i++ {
			cmd := fmt.Sprintf(
				"netsh interface ipv4 add dnsservers name=\"%s\" %s index=%d",
				lastIface, prevDNS[i], i+1,
			)
			_ = execAndLog(cmd, fmt.Sprintf("restore DNS index=%d", i+1))
		}
	}

	lastIface = ""
	prevDNS = nil
	prevDHCP = false
}

func waitForWintun(timeout time.Duration) (string, error) {
	iface, err := routing.WaitForInterfaceNameContains("wintun", timeout)
	if err != nil {
		return "", fmt.Errorf("wintun not found: %w", err)
	}
	return iface.Name, nil
}

func setInterfaceAddress(name, ip string) error {
	cmd := fmt.Sprintf(
		"netsh interface ipv4 set address name=\"%s\" source=static addr=%s mask=255.255.255.0",
		name, ip,
	)
	return execAndLog(cmd, "setInterfaceAddress")
}

func setDNS(name, dns string) error {
	cmd := fmt.Sprintf(
		"netsh interface ipv4 set dnsservers name=\"%s\" static %s",
		name, dns,
	)
	return execAndLog(cmd, "setDNS")
}

func getCurrentDNS(name string) ([]string, bool) {
	cmd := fmt.Sprintf(
		"netsh interface ipv4 show dnsservers name=\"%s\"",
		name,
	)

	out, err := routing.ExecuteCommand(cmd)
	if err != nil {
		log.Debugf(Category, "[Engine][Windows] Failed to get DNS: %v", err)
		return nil, true
	}

	lines := strings.Split(out, "\n")

	var dns []string
	isDHCP := false

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if strings.Contains(line, "DHCP") {
			isDHCP = true
		}

		if ip := net.ParseIP(line); ip != nil {
			dns = append(dns, ip.String())
		}
	}

	log.Debugf(Category, "[Engine][Windows] Current DNS: DHCP=%v DNS=%v", isDHCP, dns)

	return dns, isDHCP
}
