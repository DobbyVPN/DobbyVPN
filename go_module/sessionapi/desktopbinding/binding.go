// Package desktopbinding owns the one desktop session manager and the
// compatibility protocol lifecycle. Both the legacy API exports and gRPC
// server use this package, so neither can retain an independent CoreClient.
package desktopbinding

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	appLog "go_module/log"
	"go_module/sessionapi/runtimebridge"
	v1 "go_module/sessionapi/v1"
)

type platform struct{}

func (platform) PrepareTunnel(context.Context, v1.SessionRef) (v1.PlatformLease, error) {
	return lease{}, nil
}
func (platform) ProtectSocket(context.Context, v1.SessionRef, int) error { return nil }
func (platform) PublishState(context.Context, v1.Event) error            { return nil }

type lease struct{}

func (lease) Release(context.Context) error { return nil }

type Binding struct {
	Manager *v1.Manager
	legacy  controller
}

func New(manager *v1.Manager) *Binding {
	b := &Binding{Manager: manager}
	b.legacy.manager = manager
	return b
}

var defaultOnce sync.Once
var defaultBinding *Binding

func Default() *Binding {
	defaultOnce.Do(func() {
		defaultBinding = New(v1.NewManager(v1.ManagerOptions{Runtime: runtimebridge.New(nil), Platform: platform{}, Audit: appLog.SessionAuditSink{}}))
	})
	return defaultBinding
}
func (b *Binding) StartLegacy(ctx context.Context, protocol v1.Protocol, config string) error {
	return b.legacy.start(ctx, protocol, config)
}
func (b *Binding) StopLegacy(ctx context.Context) error         { return b.legacy.stop(ctx) }
func (b *Binding) LegacyLastFailure(ctx context.Context) string { return b.legacy.lastFailure(ctx) }

type controller struct {
	mu        sync.Mutex
	manager   *v1.Manager
	sessionID string
	commands  atomic.Uint64
	lastError string
}

func (c *controller) start(ctx context.Context, protocol v1.Protocol, config string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureSession(ctx); err != nil {
		return c.remember(err)
	}
	if err := c.stopAndWait(ctx); err != nil {
		return c.remember(err)
	}
	profile := v1.RuntimeProfile{Summary: v1.ProfileSummary{Index: 0, Protocol: protocol}, RawTOML: []byte(config), NormalizedConfig: []byte(config), NormalizedFormat: configFormat(protocol)}
	if _, err := c.manager.ConfigureCompatibilityProfile(ctx, c.sessionID, c.command("configure"), profile); err != nil {
		return c.remember(err)
	}
	started, err := c.manager.Start(ctx, c.sessionID, c.command("start"), v1.StartTarget{Mode: v1.ProfileIndex, Index: 0})
	if err != nil {
		return c.remember(err)
	}
	if err := c.waitForStarted(ctx, started.Generation); err != nil {
		return c.remember(err)
	}
	c.lastError = ""
	return nil
}
func (c *controller) stop(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sessionID == "" {
		return nil
	}
	return c.remember(c.stopAndWait(ctx))
}
func (c *controller) lastFailure(ctx context.Context) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sessionID != "" {
		if snapshot, err := c.manager.Snapshot(ctx, c.sessionID); err == nil && snapshot.LastFailure != "" {
			return string(snapshot.LastFailure)
		}
	}
	return c.lastError
}
func (c *controller) ensureSession(ctx context.Context) error {
	if c.sessionID != "" {
		return nil
	}
	id, err := c.manager.CreateSession(ctx)
	if err != nil {
		return err
	}
	c.sessionID = id
	return nil
}
func (c *controller) stopAndWait(ctx context.Context) error {
	snapshot, err := c.manager.Snapshot(ctx, c.sessionID)
	if err != nil {
		return err
	}
	if snapshot.Generation == 0 || snapshot.CleanupComplete {
		return nil
	}
	if _, err := c.manager.Stop(ctx, c.sessionID, c.command("stop"), snapshot.Generation); err != nil && v1.CodeOf(err) != v1.FailureStaleGeneration {
		return err
	}
	return c.waitForCleanup(ctx)
}
func (c *controller) waitForCleanup(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		snapshot, err := c.manager.Snapshot(ctx, c.sessionID)
		if err != nil {
			return err
		}
		if snapshot.CleanupComplete {
			return nil
		}
		select {
		case <-ctx.Done():
			return &v1.Error{Code: v1.FailureCanceled, Message: "stop cleanup was canceled"}
		case <-ticker.C:
		}
	}
}
func (c *controller) waitForStarted(ctx context.Context, generation uint64) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		snapshot, err := c.manager.Snapshot(ctx, c.sessionID)
		if err != nil {
			return err
		}
		if snapshot.Generation != generation {
			return &v1.Error{Code: v1.FailureStaleGeneration, Message: "legacy start generation is no longer active"}
		}
		switch snapshot.State {
		case v1.StateConnected:
			return nil
		case v1.StateFailed:
			code := snapshot.LastFailure
			if code == "" {
				code = v1.FailureRuntime
			}
			return &v1.Error{Code: code, Message: "legacy start failed"}
		case v1.StateIdle:
			return &v1.Error{Code: v1.FailureCanceled, Message: "legacy start stopped before connecting"}
		case v1.StateDestroyed:
			return &v1.Error{Code: v1.FailureCanceled, Message: "legacy session was destroyed while starting"}
		case v1.StateConfigured, v1.StateProbing, v1.StatePreparing, v1.StateStopping:
		}
		select {
		case <-ctx.Done():
			return &v1.Error{Code: v1.FailureCanceled, Message: "legacy start was canceled"}
		case <-ticker.C:
		}
	}
}
func (c *controller) command(operation string) string {
	return fmt.Sprintf("desktop-legacy-%s-%d", operation, c.commands.Add(1))
}
func (c *controller) remember(err error) error {
	if err != nil {
		c.lastError = "operation failed"
		var domain *v1.Error
		if errors.As(err, &domain) {
			c.lastError = domain.Message
		}
	}
	return err
}
func configFormat(protocol v1.Protocol) v1.ConfigFormat {
	switch protocol {
	case v1.ProtocolOutline:
		return v1.ConfigTransportURL
	case v1.ProtocolXray:
		return v1.ConfigJSON
	case v1.ProtocolTrustTunnel:
		return v1.ConfigTOML
	default:
		return v1.ConfigTOML
	}
}
