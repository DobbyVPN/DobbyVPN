//go:build linux || android || ios

package platform_engine

import (
	"fmt"

	"github.com/xjasonlyu/tun2socks/v2/engine"

	"go_module/log"
)

func startPlatformEngine(cfg interface{}) error {
	c := cfg.(EngineConfig)
	fd := c.FD
	proxyAddr := c.ProxyAddr

	key := &engine.Key{
		Proxy:    fmt.Sprintf("socks5://%s", proxyAddr),
		Device:   fmt.Sprintf("fd://%d", fd),
		LogLevel: "info",
		MTU:      1500,
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
