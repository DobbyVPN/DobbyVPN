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

	const mtu = 1200 // must match NEPacketTunnelNetworkSettings.mtu set in Swift

	log.Infof("[Engine] starting tun2socks fd=%d proxy=%s mtu=%d", fd, proxyAddr, mtu)

	key := &engine.Key{
		Proxy:    fmt.Sprintf("socks5://%s", proxyAddr),
		Device:   fmt.Sprintf("fd://%d", fd),
		LogLevel: "info",
		MTU:      mtu,
	}

	engine.Insert(key)
	engine.Start()
	// engine.Start() is non-blocking: it spawns goroutines and returns immediately.
	log.Infof("[Engine] engine.Start() returned (goroutines running)")
	return nil
}

func stopPlatformEngine() {}
