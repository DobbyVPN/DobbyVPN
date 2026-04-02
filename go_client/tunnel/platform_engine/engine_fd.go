//go:build linux || android || ios

package platform_engine

import (
	"fmt"
	"github.com/xjasonlyu/tun2socks/v2/engine"
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

	engine.Insert(key)
	engine.Start()
	return nil
}

func stopPlatformEngine() {}
