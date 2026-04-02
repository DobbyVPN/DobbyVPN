//go:build windows && !(android || ios)

package platform_engine

import (
	"fmt"
	"github.com/xjasonlyu/tun2socks/v2/engine"
	"go_client/log"
	"net"
	"os/exec"
	"strings"
	"time"
)

var lastIface string

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

	cmd := exec.Command("netsh", "interface", "ipv4", "set", "dnsservers", lastIface, "dhcp")
	out, _ := cmd.CombinedOutput()
	log.Infof("[Engine][Windows] Reset DNS: %s", out)
	lastIface = ""
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
	cmd := exec.Command("netsh", "interface", "ipv4", "set", "address",
		fmt.Sprintf(`name=%s`, name),
		"source=static",
		fmt.Sprintf(`addr=%s`, ip),
		"mask=255.255.255.0",
	)
	_, err := cmd.CombinedOutput()
	return err
}

func setDNS(name, dns string) error {
	cmd := exec.Command("netsh", "interface", "ipv4", "set", "dnsservers",
		fmt.Sprintf(`name=%s`, name),
		"static", dns,
	)
	_, err := cmd.CombinedOutput()
	return err
}
