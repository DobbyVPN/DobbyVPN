//go:build windows && !(android || ios)

package internal

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"core/dnscache"
	"core/log"
	"core/routing"
	"core/tunnel"
	"core/tunnel/platform_engine"
	"core/tunnel/protected_dialer"
)

var windowsRunSequence atomic.Uint64

func (app *App) Run(ctx context.Context, initResult chan<- error) (runErr error) {
	defer func() { runErr = app.finishCleanup(ctx, runErr) }()
	startedAt := time.Now()
	plan := routing.NewPlan(fmt.Sprintf("windows-%d-%d", startedAt.UnixNano(), windowsRunSequence.Add(1)))
	if app.RoutingConfig == nil {
		err := errors.New("routing config is not initialized")
		signalInit(initResult, err)
		return err
	}
	var ownedEngine *tunnel.Engine
	app.setCleanup(plan.Close, func(ctx context.Context) error {
		if ownedEngine != nil {
			return ownedEngine.Stop(ctx)
		}
		return nil
	}, func(context.Context) error { protected_dialer.ResetDefaultRoute(); return nil })

	// Capture before tunnel DNS is installed. Each later lookup captures the same
	// native physical interface source, never the VPN-overridden global resolver.
	ipv4, ipv6, err := routing.WindowsPhysicalInterfaces()
	if err != nil {
		signalInit(initResult, err)
		return err
	}
	resolverIndex := ipv4
	if resolverIndex == 0 {
		resolverIndex = ipv6
	}
	if _, _, _, err = dnscache.CaptureWindowsInterface(ctx, resolverIndex); err != nil {
		signalInit(initResult, err)
		return err
	}
	physical, err := net.InterfaceByIndex(resolverIndex)
	if err != nil {
		signalInit(initResult, err)
		return err
	}
	app.UplinkInterface = physical.Name
	protected_dialer.SetPhysicalInterfaces(ipv4, ipv6)
	ownedEngine, err = tunnel.StartOwnedEngine(platform_engine.EngineConfig{FD: -1, UplinkIface: physical.Name}, app.DNSCache, app.BypassPolicy)
	app.Engine = ownedEngine
	if err != nil {
		signalInit(initResult, err)
		return err
	}
	guard := platform_engine.WindowsNetworkProtection()
	if guard == nil {
		err = errors.New("Windows network protection owner missing")
		signalInit(initResult, err)
		return err
	}

	capture := func(queryCtx context.Context) (string, dnscache.Resolver, error) {
		v4, v6, err := routing.WindowsPhysicalInterfaces()
		if err != nil {
			protected_dialer.ResetDefaultRoute()
			return "", nil, errors.Join(err, guard.UpdateBootstrap(queryCtx, "unavailable", nil))
		}
		protected_dialer.SetPhysicalInterfaces(v4, v6)
		index := v4
		if index == 0 {
			index = v6
		}
		identity, servers, resolve, err := dnscache.CaptureWindowsInterface(queryCtx, index)
		if err != nil {
			return "", nil, errors.Join(err, guard.UpdateBootstrap(queryCtx, "unavailable", nil))
		}
		identity = fmt.Sprintf("%s:families=%d,%d", identity, v4, v6)
		if err = guard.UpdateBootstrap(queryCtx, identity, servers); err != nil {
			return "", nil, err
		}
		// Also fence the physical default selection: a DNS answer from an old link
		// must not be used after Windows has switched the preferred uplink.
		fenced := func(lookupCtx context.Context, host string) ([]net.IPAddr, error) {
			addresses, err := resolve(lookupCtx, host)
			after4, after6, inspectErr := routing.WindowsPhysicalInterfaces()
			if inspectErr != nil {
				return nil, errors.Join(err, inspectErr)
			}
			if after4 != v4 || after6 != v6 {
				return nil, errors.Join(err, errors.New("physical Windows network changed during DNS lookup"))
			}
			return addresses, err
		}
		return identity, fenced, nil
	}
	app.DNSCache.SetResolverSource(capture)
	if _, _, err = capture(ctx); err != nil {
		signalInit(initResult, err)
		return err
	}
	if err = routing.ConfigureWindowsRouting(plan, ownedEngine.InterfaceName()); err != nil {
		signalInit(initResult, err)
		return err
	}
	log.Info(Category, "Windows retained network ready", map[string]any{"interface": ownedEngine.InterfaceName(), "ipv4_interface": ipv4, "ipv6_interface": ipv6, "duration_ms": time.Since(startedAt).Milliseconds()})
	signalInit(initResult, nil)

	// Keep socket bindings and bootstrap permissions current even while no
	// protocol is working. Lookups refresh synchronously as well. This cadence
	// observes native changes; it is not a readiness or shutdown deadline.
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	previousError := ""
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			refreshCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_, _, err := capture(refreshCtx)
			cancel()
			if err != nil {
				if err.Error() != previousError {
					log.Error(Category, "Windows physical network refresh failed", map[string]any{"cause": err.Error()})
					previousError = err.Error()
				}
			} else if previousError != "" {
				log.Info(Category, "Windows physical network available", nil)
				previousError = ""
			}
		}
	}
}
