//go:build linux || android || ios

package platform_engine

import (
	"fmt"

	"github.com/xjasonlyu/tun2socks/v2/engine"

	"go_module/log"
)

func startPlatformEngine(cfg interface{}) error {
	c := cfg.(EngineConfig)

	key := &engine.Key{
		Proxy:    fmt.Sprintf("socks5://%s", c.ProxyAddr),
		Device:   fmt.Sprintf("fd://%d", c.FD),
		LogLevel: "info",
		MTU:      1200,
	}

	log.Debugf(Category, "[Engine][FD] Insert key proxy=%s device=fd://%d mtu=%d", c.ProxyAddr, c.FD, key.MTU)
	engine.Insert(key)
	log.Debugf(Category, "[Engine][FD] Start begin")
	engine.Start()
	log.Debugf(Category, "[Engine][FD] Start returned")
	return nil
}

func stopPlatformEngine() {
	log.Debugf(Category, "[Engine][FD] platform stop hook")
}
