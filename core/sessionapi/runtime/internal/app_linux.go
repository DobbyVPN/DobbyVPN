//go:build linux && !android

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

	"golang.getoutline.org/sdk/network"
)

func (app *App) validateRunInputs() error {
	if app.RoutingConfig == nil || app.DNSCache == nil || app.BypassPolicy == nil {
		return errors.New("network routing, resolver and bypass inputs are required")
	}
	return nil
}

func (app *App) Run(ctx context.Context, initResult chan<- error) (runErr error) {
	defer func() { runErr = app.finishCleanup(ctx, runErr) }()
	fail := func(err error) error { signalInit(initResult, err); return err }
	if err := app.validateRunInputs(); err != nil {
		return fail(err)
	}
	config := app.RoutingConfig
	// Retire precisely tagged state from an interrupted older process. The new
	// policy leaves physical routes untouched, so it needs no route restoration.
	if err := routing.RecoverLinuxNetworkRules(config.RoutingTableID, config.RoutingTablePriority, config.TunDeviceName); err != nil {
		return fail(err)
	}
	if err := routing.RecoverLinuxOwnedRoutes(config.RoutingTableID, config.RoutingTablePriority, config.TunDeviceName); err != nil {
		return fail(err)
	}
	index, err := routing.LinuxPhysicalInterface(config.TunDeviceName)
	if err != nil {
		return fail(err)
	}
	iface, err := net.InterfaceByIndex(index)
	if err != nil {
		return fail(err)
	}
	app.UplinkInterface = iface.Name
	if _, _, captureErr := dnscache.CaptureLinuxLink(ctx, index); captureErr != nil {
		return fail(captureErr)
	}

	plan := routing.NewPlan(config.TunDeviceName)
	var engine *tunnel.Engine
	var tun network.IPDevice
	app.setCleanup(func(cleanup context.Context) error {
		if engine != nil {
			return engine.Stop(cleanup)
		}
		return nil
	}, plan.Close, func(context.Context) error {
		if tun != nil {
			if closeErr := tun.Close(); closeErr != nil {
				return closeErr
			}
		}
		protected_dialer.SetLinuxSocketMark(0)
		return nil
	})
	guard, err := plan.AcquireLinuxOutputGuard(ctx, config.TunDeviceName, config.RoutingTableID)
	if err != nil {
		return fail(err)
	}
	capture := func(queryCtx context.Context) (string, dnscache.Resolver, error) {
		return captureLinuxResolver(queryCtx, config.TunDeviceName, guard)
	}
	if _, _, captureErr := capture(ctx); captureErr != nil {
		return fail(captureErr)
	}
	app.DNSCache.SetResolverSource(capture)
	tun, err = newTunDevice(config.TunDeviceName, config.TunDeviceIP)
	if err != nil {
		return fail(err)
	}
	descriptor, ok := tun.(interface{ GetFd() int })
	if !ok || descriptor.GetFd() < 0 {
		return fail(errors.New("owned TUN descriptor is unavailable"))
	}
	engine, err = tunnel.StartOwnedFDEngine(platform_engine.EngineConfig{FD: descriptor.GetFd()}, app.DNSCache, app.BypassPolicy)
	app.Engine = engine
	if err != nil {
		return fail(err)
	}
	if err := plan.AcquireLinuxNetwork(config.TunDeviceName, config.RoutingTableID, config.RoutingTablePriority); err != nil {
		return fail(err)
	}
	protected_dialer.SetLinuxSocketMark(config.RoutingTableID)
	if _, err := plan.AcquireLinuxResolvedDNS(ctx, config.TunDeviceName, config.DNSServers); err != nil {
		return fail(err)
	}
	log.Infof(Category, "Linux network acquired interface=%s physical=%s mark=%d IPv4=true IPv6=true protection=owned-output-filter", config.TunDeviceName, iface.Name, config.RoutingTableID)
	signalInit(initResult, nil)
	return maintainLinuxResolver(ctx, capture)
}

func captureLinuxResolver(ctx context.Context, tunName string, guard *routing.LinuxOutputGuard) (string, dnscache.Resolver, error) {
	index, err := routing.LinuxPhysicalInterface(tunName)
	if err != nil {
		return "", nil, errors.Join(err, guard.UpdateBootstrap(ctx, "unavailable", nil))
	}
	identity, servers, err := dnscache.CaptureLinuxLink(ctx, index)
	if err != nil {
		return "", nil, err
	}
	if err := guard.UpdateBootstrap(ctx, identity, servers); err != nil {
		return "", nil, err
	}
	return identity, dnscache.LinuxLinkResolver(index), nil
}

func maintainLinuxResolver(ctx context.Context, capture func(context.Context) (string, dnscache.Resolver, error)) error {
	// Physical routes need no repair. Refresh service-owned bootstrap exceptions
	// even when no protocol is running; each endpoint lookup also refreshes them.
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			refresh, cancel := context.WithTimeout(ctx, dnscache.ServerResolveTimeout)
			_, _, err := capture(refresh)
			cancel()
			if err != nil && ctx.Err() == nil {
				log.Warnf(Category, "Linux underlying network refresh: %v", fmt.Errorf("capture physical resolver: %w", err))
			}
		}
	}
}
