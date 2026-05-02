package platform_engine

import (
	"github.com/xjasonlyu/tun2socks/v2/engine"
)

type EngineConfig struct {
	ProxyAddr   string
	FD          int    // Linux / Mobile
	UplinkIface string // Windows
	MTU         int
}

func (c EngineConfig) EffectiveMTU(defaultMTU int) int {
	if c.MTU > 0 {
		return c.MTU
	}
	return defaultMTU
}

func StartPlatformEngine(cfg EngineConfig) error {
	return startPlatformEngine(cfg)
}

func EngineStop() {
	// engine.Stop() is process-global inside tun2socks; keep this visible in logs
	// because stale goroutines look like packet-flow stalls on the platform side.
	stopPlatformEngine()
	engine.Stop()
}
