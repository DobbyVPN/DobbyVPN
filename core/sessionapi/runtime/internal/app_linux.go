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

	"golang.org/x/sys/unix"
)

func (app *App) validateRunInputs() error {
	if app.RoutingConfig == nil || app.DNSCache == nil {
		return errors.New("network routing and resolver inputs are required")
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
	tunFD := -1
	app.setCleanup(func(cleanup context.Context) error {
		if engine != nil {
			return engine.Stop(cleanup)
		}
		return nil
	}, plan.Close, func(context.Context) error {
		if tunFD >= 0 {
			err := unix.Close(tunFD)
			tunFD = -1 // Linux releases the descriptor even when close reports an error.
			if err != nil {
				return fmt.Errorf("close Linux TUN: %w", err)
			}
		}
		protected_dialer.SetLinuxSocketMark(0)
		return nil
	})
	guard, err := plan.AcquireLinuxOutputGuard(ctx, config.TunDeviceName, config.RoutingTableID)
	if err != nil {
		return fail(err)
	}
	app.Guard = guard
	capture := func(queryCtx context.Context) (string, dnscache.Resolver, error) {
		return captureLinuxResolver(queryCtx, config.TunDeviceName, config.RoutingTableID)
	}
	if _, _, captureErr := capture(ctx); captureErr != nil {
		return fail(captureErr)
	}
	app.DNSCache.SetResolverSource(capture)
	tunFD, err = app.openLinuxTUN()
	if err != nil {
		return fail(err)
	}
	engine, err = tunnel.StartOwnedFDEngine(platform_engine.EngineConfig{FD: tunFD})
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
	readyFields := retainedNetworkReadyFields(app.NetworkRef, config.TunDeviceName)
	readyFields["physical"] = iface.Name
	readyFields["mark"] = config.RoutingTableID
	readyFields["ipv4"] = true
	readyFields["ipv6"] = true
	readyFields["protection"] = "owned-output-filter"
	log.Info(Category, "Linux retained network ready", readyFields)
	signalInit(initResult, nil)
	return maintainLinuxResolver(ctx, capture)
}

func captureLinuxResolver(ctx context.Context, tunName string, mark int) (string, dnscache.Resolver, error) {
	index, err := routing.LinuxPhysicalInterface(tunName)
	if err != nil {
		return "", nil, err
	}
	return dnscache.CaptureLinuxBootstrap(ctx, index, mark, routing.OpenLinuxResolverQuery)
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
