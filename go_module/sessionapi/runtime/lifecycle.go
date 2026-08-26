// Package runtime owns one transactional protocol/runtime lease for SessionV2.
// It is deliberately owned by Go: callers never interpret profile TOML or
// normalized protocol payloads.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"go_module/dnscache"
	"go_module/log"
	"go_module/probe"
	"go_module/protocol"
	v2 "go_module/sessionapi/v2"
	"go_module/tunnel"
)

const category = "sessionapi/runtime"

// defaultProbeTimeout gives a newly-created mobile tunnel enough time for the
// operating system to publish its VPN route and for tun2socks to establish the
// first protected TCP flows.  The probe still requires the same two-of-three
// endpoint quorum; this is only a bounded startup allowance.  Steady-state
// health checks continue to use their own context deadlines.
const defaultProbeTimeout = 15 * time.Second

// TunnelProvider is the deliberately narrow mobile boundary. Acquire must
// return a newly allocated TUN for this exact SessionRef; a provider must not
// retain or reuse a TUN from an earlier generation. ProtectSocket is passed to
// custom device factories so platform dial hooks can correlate protection with
// the session which owns the socket.
type TunnelProvider interface {
	Acquire(context.Context, v2.SessionRef) (TunnelLease, error)
	ProtectSocket(context.Context, v2.SessionRef, int) error
}

type TunnelLease interface {
	io.ReadWriteCloser
	Fd() uintptr
	Release(context.Context) error
}

// InputProvider owns the Go-only routing and DNS preparation inputs. It must
// return a lease which reverses exactly its session's changes. The built-in
// implementation owns tunnel's process-global exclusion policy; Runtime
// serializes all leases so no other runtime lease can overwrite that policy.
type InputProvider interface {
	Apply(context.Context, v2.SessionRef, []string, []string) (InputLease, error)
}

type InputLease interface{ Release(context.Context) error }

// DeviceFactory receives only the normalized config for every protocol.
type DeviceFactory func(context.Context, v2.SessionRef, v2.RuntimeProfile, SocketProtector) (protocol.ProtocolDevice, error)

type SocketProtector func(context.Context, int) error

type CoreFactory func(protocol.ProtocolDevice, io.ReadWriteCloser) sessionCore

type sessionCore interface {
	Connect() error
	Disconnect() error
}

type ProbeFunc func(context.Context) (int64, error)

// ConnectedHealthFunc runs one connected-readiness check for a specific lease.
// It must return promptly when ctx is canceled; the monitor uses that guarantee
// to stop before runtime resources are released.
type ConnectedHealthFunc func(context.Context, v2.SessionRef) error

type Options struct {
	Tunnel                  TunnelProvider
	Inputs                  InputProvider
	NewDevice               DeviceFactory
	NewCore                 CoreFactory
	Probe                   ProbeFunc
	ProbeTimeout            time.Duration
	InitialReadiness        ConnectedHealthFunc
	ReadinessAttempts       int
	ReadinessAttemptTimeout time.Duration
	ReadinessRetryInterval  time.Duration
	ConnectedHealth         ConnectedHealthFunc
	HealthInterval          time.Duration
	HealthFailureThreshold  int
}

// New returns a SessionV2 Runtime. Its operation mutex deliberately serializes all
// runs: platform routing and native tunnel resources are process-wide, so
// parallel profile probes would not be isolated.
func New(options Options) v2.Runtime {
	r := &runtime{options: options}
	if r.options.Inputs == nil {
		r.options.Inputs = defaultInputs{}
	}
	if r.options.NewDevice == nil {
		r.options.NewDevice = unsupportedDevice
	}
	if r.options.NewCore == nil {
		r.options.NewCore = newPlatformCore
	}
	if r.options.Probe == nil {
		r.options.Probe = defaultProbe
	}
	if r.options.ProbeTimeout <= 0 {
		r.options.ProbeTimeout = defaultProbeTimeout
	}
	if r.options.ConnectedHealth == nil {
		r.options.ConnectedHealth = defaultConnectedHealth
	}
	if r.options.InitialReadiness == nil {
		r.options.InitialReadiness = r.options.ConnectedHealth
	}
	if r.options.ReadinessAttempts <= 0 {
		r.options.ReadinessAttempts = 6
	}
	if r.options.ReadinessAttemptTimeout <= 0 {
		r.options.ReadinessAttemptTimeout = 5 * time.Second
	}
	if r.options.ReadinessRetryInterval <= 0 {
		r.options.ReadinessRetryInterval = 200 * time.Millisecond
	}
	if r.options.HealthInterval <= 0 {
		r.options.HealthInterval = 10 * time.Second
	}
	if r.options.HealthFailureThreshold <= 0 {
		r.options.HealthFailureThreshold = defaultHealthFailureThreshold()
	}
	configureTestSeams(&r.options)
	return r
}

func defaultHealthFailureThreshold() int {
	// A slow saturated link can briefly starve independent health requests even
	// while the active transfer is still making progress. Require three complete
	// failed cycles before teardown on every platform; successful checks still
	// reset the consecutive-failure count immediately.
	return 3
}

type runtime struct {
	mu      sync.Mutex
	active  bool
	options Options
}

func (r *runtime) Start(ctx context.Context, ref v2.SessionRef, profile v2.RuntimeProfile) (v2.RuntimeLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active {
		return nil, errors.New("another runtime lease is active")
	}
	r.active = true
	lease, err := r.startLocked(ctx, ref, profile)
	if err != nil {
		r.active = false
		return nil, err
	}
	if err := waitForInitialReadiness(
		ctx,
		ref,
		r.options.InitialReadiness,
		r.options.ReadinessAttempts,
		r.options.ReadinessAttemptTimeout,
		r.options.ReadinessRetryInterval,
	); err != nil {
		log.Debugf(category, "initial readiness failed generation=%d; rolling back runtime lease", ref.Generation)
		r.active = false
		cleanupErr := lease.Stop(context.Background())
		if cleanupErr != nil {
			log.Debugf(category, "initial readiness rollback failed generation=%d", ref.Generation)
		} else {
			log.Debugf(category, "initial readiness rollback complete generation=%d", ref.Generation)
		}
		return nil, errors.Join(fmt.Errorf("wait for initial tunnel readiness: %w", err), cleanupErr)
	}
	lease.setOnDone(func() {
		r.mu.Lock()
		r.active = false
		r.mu.Unlock()
	})
	lease.startHealthMonitor(ctx, ref, r.options.ConnectedHealth, r.options.HealthInterval, r.options.HealthFailureThreshold)
	return lease, nil
}

func (r *runtime) Probe(ctx context.Context, ref v2.SessionRef, profile v2.RuntimeProfile) (result v2.ProbeResult, err error) {
	if contextErr := ctx.Err(); contextErr != nil {
		return v2.ProbeResult{}, contextErr
	}
	r.mu.Lock()
	if r.active {
		r.mu.Unlock()
		return v2.ProbeResult{}, errors.New("another runtime lease is active")
	}
	r.active = true

	// A probe owns an ordinary, fresh runtime lease. It is always stopped before
	// returning, even when the health seam or context fails.
	lease, err := r.startLocked(ctx, ref, profile)
	if err != nil {
		r.active = false
		r.mu.Unlock()
		return v2.ProbeResult{}, err
	}
	lease.setOnDone(func() {
		r.mu.Lock()
		r.active = false
		r.mu.Unlock()
	})
	r.mu.Unlock()
	defer func() {
		if cleanupErr := lease.Stop(context.Background()); cleanupErr != nil {
			result = v2.ProbeResult{}
			err = errors.Join(err, fmt.Errorf("cleanup runtime probe: %w", cleanupErr))
		}
	}()

	probeCtx, cancel := context.WithTimeout(ctx, r.options.ProbeTimeout)
	defer cancel()
	latency, probeErr := probeUntilReady(
		probeCtx,
		ref,
		r.options.Probe,
		r.options.ReadinessAttempts,
		r.options.ReadinessRetryInterval,
	)
	if probeErr != nil {
		return v2.ProbeResult{}, probeErr
	}
	return v2.ProbeResult{LatencyMillis: latency}, nil
}

// probeUntilReady gives Android's newly established VPN route the same bounded
// readiness tolerance as the final session start. Fast protocol devices can
// begin their first request a few milliseconds before ConnectivityService has
// published the VPN network; a failed quorum is retried inside the existing
// overall ProbeTimeout rather than being misclassified as a dead profile.
func probeUntilReady(
	ctx context.Context,
	ref v2.SessionRef,
	probe ProbeFunc,
	attempts int,
	retryInterval time.Duration,
) (int64, error) {
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		startedAt := time.Now()
		latency, err := probe(ctx)
		if err != nil {
			return 0, err
		}
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if latency >= 0 {
			log.Debugf(category, "runtime probe readiness succeeded generation=%d attempt=%d/%d elapsed=%s", ref.Generation, attempt, attempts, time.Since(startedAt).Truncate(time.Millisecond))
			return latency, nil
		}
		log.Debugf(category, "runtime probe readiness failed generation=%d attempt=%d/%d elapsed=%s", ref.Generation, attempt, attempts, time.Since(startedAt).Truncate(time.Millisecond))
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, ctxErr
		}
		if attempt == attempts {
			return 0, errors.New("runtime health probe did not reach quorum")
		}
		timer := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return 0, ctx.Err()
		case <-timer.C:
		}
	}
	return 0, errors.New("runtime health probe did not reach quorum")
}

func (r *runtime) startLocked(ctx context.Context, ref v2.SessionRef, profile v2.RuntimeProfile) (*lease, error) {
	if len(profile.NormalizedConfig) == 0 {
		return nil, errors.New("runtime profile has no normalized config")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	owned := &lease{}
	fail := func(cause error) (*lease, error) {
		return nil, errors.Join(cause, owned.Stop(context.Background()))
	}

	inputs, err := r.options.Inputs.Apply(ctx, ref, profile.ExcludeCIDRs, profile.PreflightHosts)
	if err != nil {
		return fail(fmt.Errorf("prepare Go routing/DNS inputs: %w", err))
	}
	if inputs == nil {
		return fail(errors.New("prepare Go routing/DNS inputs returned nil lease"))
	}
	owned.push(inputs.Release)
	protect := func(protectCtx context.Context, fd int) error {
		if r.options.Tunnel == nil {
			return nil // desktop protocol devices use their existing routing path.
		}
		if protectErr := r.options.Tunnel.ProtectSocket(protectCtx, ref, fd); protectErr != nil {
			return fmt.Errorf("protect socket for session generation %d: %w", ref.Generation, protectErr)
		}
		return nil
	}
	device, err := r.options.NewDevice(ctx, ref, profile, protect)
	if err != nil {
		return fail(fmt.Errorf("create protocol device: %w", err))
	}
	if device == nil {
		return fail(errors.New("create protocol device returned nil"))
	}

	var tun TunnelLease
	if mobileRuntime {
		if r.options.Tunnel == nil {
			return fail(errors.New("mobile runtime requires a TunnelProvider"))
		}
		tun, err = r.options.Tunnel.Acquire(ctx, ref)
		if err != nil {
			return fail(fmt.Errorf("acquire fresh TUN: %w", err))
		}
		// Acquisition is recorded immediately, before any later construction.
		if tun == nil {
			return fail(errors.New("acquire fresh TUN returned nil lease"))
		}
		owned.push(tun.Release)
	}

	client := r.options.NewCore(device, tun)
	if client == nil {
		return fail(errors.New("create native session runtime returned nil"))
	}
	// A partially connected native runtime can own a device/engine, so register its
	// rollback before Connect rather than only after Connect reports success.
	owned.push(func(context.Context) error { return client.Disconnect() })
	if err := connectContext(ctx, client); err != nil {
		return fail(fmt.Errorf("connect transactional native session runtime: %w", err))
	}
	// The native runtime is stopped before the input lease and mobile TUN in strict LIFO
	// order. This preserves the tun2socks/device dependency chain.
	log.Debugf(category, "runtime connected protocol=%s generation=%d", profile.Summary.Protocol, ref.Generation)
	return owned, nil
}

func waitForInitialReadiness(
	ctx context.Context,
	ref v2.SessionRef,
	check ConnectedHealthFunc,
	attempts int,
	attemptTimeout time.Duration,
	retryInterval time.Duration,
) error {
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		startedAt := time.Now()
		log.Debugf(category, "initial readiness attempt begin generation=%d attempt=%d/%d timeout=%s", ref.Generation, attempt, attempts, attemptTimeout)
		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		err := check(attemptCtx, ref)
		cancel()
		if err == nil {
			log.Debugf(category, "initial readiness attempt succeeded generation=%d attempt=%d/%d elapsed=%s", ref.Generation, attempt, attempts, time.Since(startedAt).Truncate(time.Millisecond))
			return nil
		}
		outcome := "check_failed"
		if errors.Is(err, context.DeadlineExceeded) {
			outcome = "timeout"
		} else if errors.Is(err, context.Canceled) {
			outcome = "canceled"
		}
		log.Debugf(category, "initial readiness attempt failed generation=%d attempt=%d/%d outcome=%s elapsed=%s", ref.Generation, attempt, attempts, outcome, time.Since(startedAt).Truncate(time.Millisecond))
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		lastErr = err
		if attempt == attempts {
			break
		}
		timer := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	return fmt.Errorf("readiness failed after %d attempts: %w", attempts, lastErr)
}

func connectContext(ctx context.Context, client sessionCore) error {
	result := make(chan error, 1)
	go func() { result <- client.Connect() }()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		disconnectErr := client.Disconnect()
		// Do not return a failed start while Connect can still publish a late
		// successful core. The native runtime's Disconnect cancels its bounded startup;
		// waiting here makes cancellation ownership deterministic.
		connectErr := <-result
		return errors.Join(ctx.Err(), disconnectErr, connectErr)
	}
}

type lease struct {
	mu           sync.Mutex
	stopped      bool
	undo         []func(context.Context) error
	onDone       func()
	done         chan struct{}
	cleanupErr   error
	healthCancel context.CancelFunc
	healthDone   chan struct{}
	healthFailed chan struct{}
}

func (l *lease) push(fn func(context.Context) error) { l.undo = append(l.undo, fn) }
func (l *lease) setOnDone(fn func())                 { l.onDone = fn }

// HealthFailures implements SessionV2 HealthMonitoringLease. It is closed after the
// lease has stopped, so the manager watcher cannot outlive its runtime lease.
func (l *lease) HealthFailures() <-chan struct{} { return l.healthFailed }

func (l *lease) startHealthMonitor(parent context.Context, ref v2.SessionRef, check ConnectedHealthFunc, interval time.Duration, threshold int) {
	ctx, cancel := context.WithCancel(parent)
	l.healthCancel = cancel
	l.healthDone = make(chan struct{})
	l.healthFailed = make(chan struct{}, 1)
	go func() {
		defer close(l.healthDone)
		defer close(l.healthFailed)
		failures := 0
		for {
			if err := check(ctx, ref); err != nil {
				failures++
				if failures >= threshold {
					select {
					case l.healthFailed <- struct{}{}:
					case <-ctx.Done():
					}
					return
				}
			} else {
				failures = 0
			}
			if interval <= 0 {
				select {
				case <-ctx.Done():
					return
				default:
				}
				continue
			}
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return
			case <-timer.C:
			}
		}
	}()
}

func (l *lease) Stop(ctx context.Context) error {
	l.mu.Lock()
	if l.stopped {
		done := l.done
		l.mu.Unlock()
		<-done
		l.mu.Lock()
		err := l.cleanupErr
		l.mu.Unlock()
		return err
	}
	l.stopped = true
	l.done = make(chan struct{})
	undo := l.undo
	onDone := l.onDone
	l.undo = nil
	l.onDone = nil
	healthCancel, healthDone := l.healthCancel, l.healthDone
	l.healthCancel = nil
	l.mu.Unlock()
	if healthCancel != nil {
		healthCancel()
	}
	if healthDone != nil {
		<-healthDone
	}
	var errs []error
	for i := len(undo) - 1; i >= 0; i-- {
		if err := undo[i](ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if onDone != nil {
		onDone()
	}
	err := errors.Join(errs...)
	l.mu.Lock()
	l.cleanupErr = err
	close(l.done)
	l.mu.Unlock()
	return err
}

type defaultInputs struct{}

func (defaultInputs) Apply(ctx context.Context, _ v2.SessionRef, cidrs, hosts []string) (InputLease, error) {
	routes, err := tunnel.AcquireGeoRoutingConf(cidrs)
	if err != nil {
		return nil, fmt.Errorf("acquire exclusion policy: %w", err)
	}
	profileResolved := 0
	for _, host := range hosts {
		if err := ctx.Err(); err != nil {
			routes.Release()
			return nil, err
		}
		if _, err := dnscache.ResolveIPv4(
			ctx,
			host,
			dnscache.FastResolveTimeout,
			"runtime-profile-preflight",
		); err != nil {
			// DNS prewarm is best-effort; protocol DNS remains authoritative.
			log.Debugf(category, "preflight DNS did not resolve host count=%d", len(hosts))
		} else {
			profileResolved++
		}
	}
	probeResolved, probeTotal := probe.PreflightTunnelProbeDNS(ctx)
	log.Debugf(
		category,
		"preflight DNS complete profileResolved=%d profileTotal=%d probeResolved=%d probeTotal=%d",
		profileResolved,
		len(hosts),
		probeResolved,
		probeTotal,
	)
	return routingInputs{routes: routes}, nil
}

type routingInputs struct{ routes *tunnel.GeoRoutingLease }

func (l routingInputs) Release(context.Context) error { l.routes.Release(); return nil }

func unsupportedDevice(_ context.Context, _ v2.SessionRef, _ v2.RuntimeProfile, _ SocketProtector) (protocol.ProtocolDevice, error) {
	return nil, errors.New("native protocol device factory is not installed")
}

func defaultProbe(ctx context.Context) (int64, error) {
	timeout, err := probeTimeout(ctx)
	if err != nil {
		return 0, err
	}
	latency := probe.MeasureTunnelProbeAverageLatencyMillisWithContext(ctx, int64(timeout/time.Millisecond))
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return latency, nil
}

func probeTimeout(ctx context.Context) (time.Duration, error) {
	const defaultTimeout = 5 * time.Second
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return defaultTimeout, nil
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0, ctx.Err()
	}
	return remaining, nil
}

func defaultConnectedHealth(ctx context.Context, _ v2.SessionRef) error {
	latency, err := defaultProbe(ctx)
	if err != nil {
		return err
	}
	if latency < 0 {
		return errors.New("runtime connected health check did not reach quorum")
	}
	return nil
}
