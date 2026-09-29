package platform_engine

import (
	"github.com/xjasonlyu/tun2socks/v2/engine"
)

type EngineConfig struct {
	ProxyAddr   string
	FD          int    // Linux / Mobile
	UplinkIface string // Windows
}

func StartPlatformEngine(cfg EngineConfig) error {
	return startPlatformEngine(cfg)
}

// EngineStop lets each platform preserve its required teardown order around
// tun2socks' device close and returns only after platform cleanup is complete.
func EngineStop() error {
	return stopPlatformEngine(engine.Stop)
}

func InterfaceName() string { return platformInterfaceName() }
