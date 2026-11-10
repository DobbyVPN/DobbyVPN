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
	"core/sessionapi"
	"core/tunnel"
	"core/tunnel/platform_engine"
	"core/tunnel/protected_dialer"
)

func macOSNetworkFields(physical routing.MacOSPhysicalNetwork, ref sessionapi.SessionRef) map[string]any {
	return map[string]any{
		"session_id":                    ref.SessionID,
		"network_generation":            ref.Generation,
		"network_identity":              physical.Identity,
		"physical_ipv4_interface_index": physical.IPv4,
		"physical_ipv6_interface_index": physical.IPv6,
		"ipv4_available":                physical.IPv4 > 0,
		"ipv6_available":                physical.IPv6 > 0,
	}
}

func macOSReadyFields(physical routing.MacOSPhysicalNetwork, ref sessionapi.SessionRef, name string) map[string]any {
	fields := macOSNetworkFields(physical, ref)
	fields["interface"] = name
	return fields
}

func macOSRefreshFields(physical routing.MacOSPhysicalNetwork, ref sessionapi.SessionRef, repaired bool) map[string]any {
	fields := macOSNetworkFields(physical, ref)
	fields["routes_repaired"] = repaired
	return fields
}

func macOSMaintenanceFailureFields(physical routing.MacOSPhysicalNetwork, ref sessionapi.SessionRef, name string, source string, err error) map[string]any {
	fields := macOSReadyFields(physical, ref, name)
	fields["cause"] = err.Error()
	fields["network_observation"] = source
	return fields
}

func (app *App) Run(ctx context.Context, initResult chan<- error) (runErr error) {
	defer func() { runErr = app.finishCleanup(ctx, runErr) }()
	if app.RoutingConfig == nil {
		err := errors.New("routing config is not initialized")
		signalInit(initResult, err)
		return err
	}
	plan := routing.NewPlan(fmt.Sprintf("darwin:%p", app))
	var ownedEngine *tunnel.Engine
	app.setCleanup(namedRelease{"plan-close", plan.Close}, namedRelease{"engine-stop", func(ctx context.Context) error {
		if ownedEngine != nil {
			return ownedEngine.Stop(ctx)
		}
		return nil
	}}, namedRelease{"route-reset", func(context.Context) error { protected_dialer.ResetDefaultRoute(); return nil }})

	physicalRoutes, err := plan.AcquireMacOSPhysicalRoutes()
	if err != nil {
		signalInit(initResult, err)
		return err
	}

	capturePhysical := func(queryCtx context.Context) (routing.MacOSPhysicalNetwork, dnscache.Resolver, error) {
		physical, err := routing.CaptureMacOSPhysicalNetwork(queryCtx)
		if err != nil {
			protected_dialer.ResetDefaultRoute()
			return routing.MacOSPhysicalNetwork{}, nil, err
		}
		if err := physicalRoutes.Update(queryCtx, physical); err != nil {
			protected_dialer.ResetDefaultRoute()
			return physical, nil, err
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
		return physical, fenced, nil
	}
	capture := func(queryCtx context.Context) (string, dnscache.Resolver, error) {
		physical, resolver, err := capturePhysical(queryCtx)
		if err != nil {
			return "", nil, err
		}
		return physical.Identity, resolver, nil
	}
	// Capture the physical service before publishing the owned supplemental DNS.
	// The same source is refreshed for every endpoint lookup and link observation.
	physical, _, err := capturePhysical(ctx)
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
	log.Info(Category, "macOS retained network ready", macOSReadyFields(physical, app.NetworkRef, name))
	signalInit(initResult, nil)

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	previousError := ""
	var previousFailureNetwork routing.MacOSPhysicalNetwork
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			// Physical service transitions may remove interface routes. Repair only
			// the exact routes owned by this network; never replace foreign state.
			repaired, repairErr := plan.Repair()
			refreshCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			current, _, refreshErr := capturePhysical(refreshCtx)
			cancel()
			if err := errors.Join(repairErr, refreshErr); err != nil {
				observed := physical
				source := "last_successful"
				if current.Identity != "" {
					observed = current
					source = "current"
				}
				if err.Error() != previousError || observed != previousFailureNetwork {
					log.Error(Category, "macOS network maintenance failed; interface retained", macOSMaintenanceFailureFields(observed, app.NetworkRef, name, source, err))
					previousError = err.Error()
					previousFailureNetwork = observed
				}
			} else {
				if current != physical || repaired || previousError != "" {
					log.Info(Category, "macOS physical network refreshed", macOSRefreshFields(current, app.NetworkRef, repaired))
					physical = current
				}
				previousError = ""
			}
		}
	}
}
