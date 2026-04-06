//go:build windows && !(android || ios)

package platform_engine

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/xjasonlyu/tun2socks/v2/engine"
	"go_module/log"
	"go_module/routing"
)

var (
	lastIface string
	prevDNS   []string
	prevDHCP  bool
)

func execAndLog(cmd string, context string) error {
	out, err := routing.ExecuteCommand(cmd)
	if err != nil {
		log.Infof("[Engine][Windows][ERROR] %s: %v | output=%s",
			context, err, out,
		)
		return err
	}

	log.Infof("[Engine][Windows][OK] %s: %s", context, out)
	return nil
}

func startPlatformEngine(cfg interface{}) error {
	c := cfg.(EngineConfig)
	proxyAddr := c.ProxyAddr
	uplinkIface := c.UplinkIface

	log.Infof("[Engine][Windows] proxy=%s iface=%s", proxyAddr, uplinkIface)

	key := &engine.Key{
		Proxy:     fmt.Sprintf("socks5://%s", proxyAddr),
		Device:    "wintun",
		Interface: uplinkIface,
		LogLevel:  "info",
		MTU:       1500,
	}

	engine.Insert(key)
	engine.Start()

	ifName, err := waitForWintun(5 * time.Second)
	if err != nil {
		engine.Stop()
		return err
	}

	lastIface = ifName

	prevDNS, prevDHCP = getCurrentDNS(ifName)

	if err := setInterfaceAddress(ifName, "10.0.85.2"); err != nil {
		engine.Stop()
		return err
	}
	if err := setDNS(ifName, "1.1.1.1"); err != nil {
		engine.Stop()
		return err
	}

	return nil
}

func stopPlatformEngine() {
	if lastIface == "" {
		return
	}

	log.Infof("[Engine][Windows] Restoring DNS. DHCP=%v DNS=%v", prevDHCP, prevDNS)

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
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ifaces, _ := net.Interfaces()
		for _, ifc := range ifaces {
			if strings.Contains(strings.ToLower(ifc.Name), "wintun") {
				return ifc.Name, nil
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return "", fmt.Errorf("wintun not found")
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
		log.Infof("[Engine][Windows] Failed to get DNS: %v", err)
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

		// ищем IP
		if ip := net.ParseIP(line); ip != nil {
			dns = append(dns, ip.String())
		}
	}

	log.Infof("[Engine][Windows] Current DNS: DHCP=%v DNS=%v", isDHCP, dns)

	return dns, isDHCP
}
