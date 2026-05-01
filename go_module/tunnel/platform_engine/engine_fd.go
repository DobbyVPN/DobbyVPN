//go:build linux || android || ios

package platform_engine

import (
	"fmt"

	"github.com/xjasonlyu/tun2socks/v2/engine"

	"go_module/log"
)

func startPlatformEngine(cfg interface{}) error {
	c := cfg.(EngineConfig)

	const mtu = 1200 // must match NEPacketTunnelNetworkSettings.mtu set in Swift

	log.Infof("[Engine] starting tun2socks fd=%d proxy=%s mtu=%d", fd, proxyAddr, mtu)

	key := &engine.Key{
		Proxy:    fmt.Sprintf("socks5://%s", c.ProxyAddr),
		Device:   fmt.Sprintf("fd://%d", c.FD),
		LogLevel: "info",
		MTU:      mtu,
	}

	log.Infof("[Engine][FD] Insert key proxy=%s device=fd://%d mtu=%d", c.ProxyAddr, c.FD, key.MTU)
	engine.Insert(key)
	log.Infof("[Engine][FD] Start begin")
	engine.Start()
	log.Infof("[Engine][FD] Start returned")
	return nil
}

func stopPlatformEngine() {
	log.Infof("[Engine][FD] platform stop hook")
}
