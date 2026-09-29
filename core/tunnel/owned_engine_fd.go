//go:build linux || android || ios

package tunnel

import (
	"errors"
	"fmt"

	"core/dnscache"
	"core/tunnel/platform_engine"

	"golang.org/x/sys/unix"
)

type ownedFDStarter func(platform_engine.EngineConfig, *dnscache.Cache, *BypassPolicy) (*Engine, bool, error)

// StartOwnedFDEngine starts tun2socks with an independently owned duplicate of
// cfg.FD. The caller always retains ownership of the original descriptor. This
// function owns the duplicate from creation onward and closes it exactly once:
// directly if the platform rejects it, or through Engine.Stop after acceptance.
func StartOwnedFDEngine(cfg platform_engine.EngineConfig, dnsCache *dnscache.Cache, bypass *BypassPolicy) (*Engine, error) {
	engineMu.Lock()
	defer engineMu.Unlock()
	if activeEngine != nil {
		return nil, ErrEngineBusy
	}
	return startOwnedFDEngineLocked(cfg, dnsCache, bypass, startOwnedEngineLocked)
}

func startOwnedFDEngineLocked(cfg platform_engine.EngineConfig, dnsCache *dnscache.Cache, bypass *BypassPolicy, start ownedFDStarter) (*Engine, error) {
	if dnsCache == nil {
		return nil, errors.New("DNS cache is required for tunnel start")
	}
	if bypass == nil {
		return nil, errors.New("bypass policy is required for tunnel start")
	}
	engineFD, err := unix.Dup(cfg.FD)
	if err != nil {
		return nil, fmt.Errorf("duplicate TUN descriptor for tun2socks: %w", err)
	}
	cfg.FD = engineFD
	handle, accepted, startErr := start(cfg, dnsCache, bypass)
	if startErr != nil && !accepted {
		return nil, errors.Join(startErr, unix.Close(engineFD))
	}
	return handle, startErr
}
