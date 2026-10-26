//go:build darwin && !(android || ios)

package internal

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"core/dnscache"
	"core/log"
	"core/routing"
	"core/tunnel"
	"core/tunnel/platform_engine"
	"core/tunnel/protected_dialer"
)

func (app *App) Run(ctx context.Context, initResult chan<- error) (runErr error) {
	defer func() { runErr = app.finishCleanup(ctx, runErr) }()
	if app.RoutingConfig == nil {
		err := errors.New("routing config is not initialized")
		signalInit(initResult, err)
		return err
	}
	plan := routing.NewPlan(fmt.Sprintf("darwin:%p", app))
	var ownedEngine *tunnel.Engine
	app.setCleanup(plan.Close, func(ctx context.Context) error {
		if ownedEngine != nil {
			return ownedEngine.Stop(ctx)
		}
		return nil
	}, func(context.Context) error { protected_dialer.ResetDefaultRoute(); return nil })

	capture := func(queryCtx context.Context) (string, dnscache.Resolver, error) {
		physical, err := routing.CaptureMacOSPhysicalNetwork(queryCtx)
		if err != nil {
			protected_dialer.ResetDefaultRoute()
			return "", nil, err
		}
		protected_dialer.SetPhysicalInterfaces(physical.IPv4, physical.IPv6)
		index := physical.IPv4
		if index == 0 {
			index = physical.IPv6
		}
		resolve := dnscache.AppleLinkResolver(index)
		fenced := func(lookupCtx context.Context, host string) ([]net.IPAddr, error) {
			addresses, err := resolve(lookupCtx, host)
			after, inspectErr := routing.CaptureMacOSPhysicalNetwork(lookupCtx)
			if inspectErr != nil {
				return nil, errors.Join(err, inspectErr)
			}
			if after.Identity != physical.Identity {
				return nil, errors.Join(err, errors.New("physical macOS network changed during DNS lookup"))
			}
			return addresses, err
		}
		return physical.Identity, fenced, nil
	}
	// Capture the physical service before publishing the owned supplemental DNS.
	// The same source is refreshed for every endpoint lookup and link observation.
	identity, _, err := capture(ctx)
	if err != nil {
		signalInit(initResult, err)
		return err
	}
	app.DNSCache.SetResolverSource(capture)
	ownedEngine, err = tunnel.StartOwnedEngine(platform_engine.EngineConfig{FD: -1}, app.DNSCache, app.BypassPolicy)
	app.Engine = ownedEngine
	if err != nil {
		signalInit(initResult, err)
		return err
	}
	name := ownedEngine.InterfaceName()
	if name == "" {
		err = errors.New("new macOS packet stack has no interface")
		signalInit(initResult, err)
		return err
	}
	if err = plan.AcquireMacOSIPv4Default(ctx, name); err == nil {
		err = plan.AcquireMacOSIPv6Default(ctx, name)
	}
	if err == nil {
		_, err = plan.AcquireMacOSDNS(ctx, name, app.RoutingConfig.DNSServers)
	}
	if err != nil {
		signalInit(initResult, err)
		return err
	}
	log.Info(Category, "macOS retained network ready", map[string]any{"interface": name, "network_identity": identity})
	signalInit(initResult, nil)

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	previousError := ""
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			// Physical service transitions may remove interface routes. Repair only
			// the exact routes owned by this network; never replace foreign state.
			repaired, repairErr := plan.Repair()
			refreshCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			currentIdentity, _, refreshErr := capture(refreshCtx)
			cancel()
			if err := errors.Join(repairErr, refreshErr); err != nil {
				if err.Error() != previousError {
					log.Error(Category, "macOS network maintenance failed; interface retained", map[string]any{"interface": name, "cause": err.Error()})
					previousError = err.Error()
				}
			} else {
				if currentIdentity != identity || repaired || previousError != "" {
					log.Info(Category, "macOS physical network refreshed", map[string]any{"network_identity": currentIdentity, "routes_repaired": repaired})
					identity = currentIdentity
				}
				previousError = ""
			}
		}
	}
}
