//go:build linux || android || ios

package tunnel

import (
	"errors"
	"fmt"

	"go_module/tunnel/platform_engine"

	"golang.org/x/sys/unix"
)

type ownedFDStarter func(platform_engine.EngineConfig) (*Engine, bool, error)

// StartOwnedFDEngine starts tun2socks with an independently owned duplicate of
// cfg.FD. The caller always retains ownership of the original descriptor. This
// function owns the duplicate from creation onward and closes it exactly once:
// directly if the platform rejects it, or through Engine.Stop after acceptance.
func StartOwnedFDEngine(cfg platform_engine.EngineConfig) (*Engine, error) {
	engineMu.Lock()
	defer engineMu.Unlock()
	if activeEngine != nil {
		return nil, ErrEngineBusy
	}
	return startOwnedFDEngineLocked(cfg, startOwnedEngineLocked)
}

func startOwnedFDEngineLocked(cfg platform_engine.EngineConfig, start ownedFDStarter) (*Engine, error) {
	engineFD, err := unix.Dup(cfg.FD)
	if err != nil {
		return nil, fmt.Errorf("duplicate TUN descriptor for tun2socks: %w", err)
	}
	cfg.FD = engineFD
	handle, accepted, startErr := start(cfg)
	if startErr != nil && !accepted {
		return nil, errors.Join(startErr, unix.Close(engineFD))
	}
	return handle, startErr
}
