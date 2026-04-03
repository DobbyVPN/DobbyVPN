//go:build darwin && !(android || ios)

package platform_engine

import (
	"fmt"
	"net"
	"os/exec"
	"time"

	"github.com/xjasonlyu/tun2socks/v2/engine"
	"go_client/log"
)

var LastIface string

func startPlatformEngine(cfg interface{}) error {
	c := cfg.(EngineConfig)
	proxyAddr := c.ProxyAddr

	deviceName := "utun233"
	LastIface = deviceName

	log.Infof("[Engine][Darwin] proxy=%s device=%s", proxyAddr, deviceName)

	key := &engine.Key{
		Proxy:    fmt.Sprintf("socks5://%s", proxyAddr),
		Device:   deviceName,
		LogLevel: "info",
		MTU:      1500,
	}

	engine.Insert(key)
	engine.Start()

	time.Sleep(500 * time.Millisecond)

	ifaces, _ := net.Interfaces()
	found := false
	for _, ifc := range ifaces {
		if ifc.Name == deviceName {
			found = true
			break
		}
	}
	if !found {
		engine.Stop()
		return fmt.Errorf("utun interface not found: %s", deviceName)
	}

	// Setting IP
	cmd := exec.Command(
		"ifconfig",
		deviceName,
		"inet",
		"198.18.0.1",
		"198.18.0.2",
		"netmask",
		"255.255.0.0",
		"up",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		engine.Stop()
		return fmt.Errorf("ifconfig failed: %w (%s)", err, out)
	}

	return nil
}

func stopPlatformEngine() {
	if LastIface == "" {
		return
	}
	LastIface = ""
}
