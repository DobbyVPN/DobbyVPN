//go:build linux || android || ios

package platform_engine

import (
	"fmt"
	"runtime"

	"github.com/xjasonlyu/tun2socks/v2/engine"

	"go_module/log"
)

const (
	defaultFDMTU = 1500
	iosFDMTU     = 1200
)

func startPlatformEngine(cfg interface{}) error {
	c := cfg.(EngineConfig)
	fd := c.FD
	proxyAddr := c.ProxyAddr
	mtu := c.MTU
	if mtu <= 0 {
		mtu = defaultPlatformMTU()
	}

	key := &engine.Key{
		Proxy:    fmt.Sprintf("socks5://%s", proxyAddr),
		Device:   fmt.Sprintf("fd://%d", fd),
		LogLevel: "info",
		MTU:      mtu,
	}

	log.Infof("[Engine][FD] Insert key proxy=%s device=fd://%d mtu=%d", proxyAddr, fd, key.MTU)
	engine.Insert(key)
	log.Infof("[Engine][FD] Start begin")
	engine.Start()
	log.Infof("[Engine][FD] Start returned")
	return nil
}

func stopPlatformEngine() {
	log.Infof("[Engine][FD] platform stop hook")
}

func defaultPlatformMTU() int {
	if runtime.GOOS == "ios" {
		return iosFDMTU
	}
	return defaultFDMTU
}
