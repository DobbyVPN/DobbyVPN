// Package runtime owns a VPN network and its independently retired protocol attempts.
// It is deliberately owned by Go: callers never interpret profile TOML or
// normalized protocol payloads.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"core/dnscache"
	"core/log"
	"core/probe"
	"core/protocol"
	"core/sessionapi"
	"core/tunnel"
	"core/tunnel/protected_dialer"
)

const category = "sessionapi/runtime"

// TunnelProvider acquires one interface for a network generation. The returned
// lease survives all protocol attempts until the network owner is stopped. ProtectSocket is passed to
// custom device factories so platform dial hooks can correlate protection with
// the session which owns the socket.
type TunnelProvider interface {
	Acquire(context.Context, sessionapi.SessionRef) (TunnelLease, error)
	ProtectSocket(context.Context, sessionapi.SessionRef, int) error
}

type NetworkResolverProvider interface {
	ConfigureResolver(context.Context, sessionapi.SessionRef, *dnscache.Cache) error
}

type TunnelLease interface {
	io.ReadWriteCloser
	Fd() uintptr
	Release(context.Context) error
}

// InputProvider resolves the bypass CIDRs and hostnames for one start attempt.
// Its returned policy is immutable and is carried by that attempt's tunnel.
type InputProvider interface {
	Resolve(context.Context, []string) (*tunnel.BypassPolicy, error)
}

// DeviceFactory receives only the normalized config for every protocol and
// the resolver context owned by the retained VPN network.
type DeviceFactory func(context.Context, sessionapi.SessionRef, sessionapi.RuntimeProfile, SocketProtector, *dnscache.Cache) (protocol.ProtocolDevice, error)

type SocketProtector func(context.Context, int) error

type CoreFactory func(io.ReadWriteCloser, *dnscache.Cache, *tunnel.BypassPolicy) sessionCore

type sessionCore interface {
	Connect(context.Context) error
	Disconnect(context.Context) error
	Attach(context.Context, uint64, string, *dnscache.Cache, *tunnel.BypassPolicy) error
	Detach(context.Context, uint64) error
	ProtocolParameters() (int, string)
}

// connectCanceler is implemented by native runtimes whose Connect operation
// owns a mutex while calling a platform API that does not accept a context.
// Requesting cancellation must not call Disconnect: the latter may need the
// same mutex and would deadlock behind the in-flight startup.  Connect remains
// the sole owner of native mutations until it returns, after which the normal
// lease rollback calls Disconnect.
type connectCanceler interface{ CancelConnect() }

// errConnectCancellationPending means Connect still owns the native startup
// operation.  The acquired lease must be transferred to its caller so the
// caller can fence later generations and run the ordinary LIFO cleanup only
// after Connect has stopped mutating native resources.
var errConnectCancellationPending = errors.New("native session connect cancellation pending")

// ConnectedHealthFunc runs one connected-readiness check for a specific lease.
// It must return promptly when ctx is canceled; the monitor uses that guarantee
// to stop before runtime resources are released.
type ConnectedHealthFunc func(context.Context, sessionapi.SessionRef, string) error

type Options struct {
	Tunnel                  TunnelProvider
	Inputs                  InputProvider
	NewDevice               DeviceFactory
	NewCore                 CoreFactory
	InitialReadiness        ConnectedHealthFunc
	ReadinessAttempts       int
	ReadinessAttemptTimeout time.Duration
	ReadinessRetryInterval  time.Duration
	ConnectedHealth         ConnectedHealthFunc
	HealthInterval          time.Duration
	HealthFailureThreshold  int
}

// New returns a session Runtime. Its operation mutex deliberately serializes all
// runs: native tunnel resources are process-wide, so parallel profile starts
// would not be isolated.
func New(options Options) sessionapi.Runtime {
	r := &runtime{options: options}
	if r.options.Inputs == nil {
		r.options.Inputs = defaultInputProvider{}
	}
	if r.options.NewDevice == nil {
		r.options.NewDevice = unsupportedDevice
	}
	if r.options.NewCore == nil {
		r.options.NewCore = newPlatformCore
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
	network *networkOwner
}

func markCleanupFailure(err error) error {
	if err == nil {
		return nil
	}
	return &sessionapi.CleanupFailure{Err: err}
}

// networkOwner is acquired before any protocol. Failed partial acquisition is
// returned to the manager, which releases it after attempt cleanup.
type networkOwner struct {
	ref   sessionapi.SessionRef
	cache *dnscache.Cache
	core  sessionCore
	lease lease
}

func (n *networkOwner) Stop(ctx context.Context) error { return n.lease.Stop(ctx) }

func (r *runtime) PrepareNetwork(ctx context.Context, ref sessionapi.SessionRef, profile sessionapi.RuntimeProfile) (sessionapi.RuntimeLease, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.network != nil || r.active {
		return nil, errors.New("previous network is still owned")
	}
	n := &networkOwner{ref: ref, cache: dnscache.New()}
	r.network = n
	n.lease.setOnDone(func(error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.network == n {
			r.network = nil
		}
	})
	// The stack starts without a transport or bypass target. Resolve the actual
	// attempt policy only after native setup has captured the physical resolver.
	entries := profile.ExcludeCIDRs
	if _, native := r.options.Inputs.(defaultInputProvider); native {
		entries = nil
	}
	bypass, err := r.options.Inputs.Resolve(ctx, entries)
	if err != nil {
		return n, fmt.Errorf("resolve network bypass: %w", err)
	}
	if bypass == nil {
		return n, errors.New("network bypass is unavailable")
	}
	var tun TunnelLease
	if mobileRuntime {
		tun, err = r.acquireNetworkTunnel(ctx, ref, n)
		if err != nil {
			return n, err
		}
	}
	n.core = r.options.NewCore(tun, n.cache, bypass)
	if n.core == nil {
		return n, errors.New("network core is unavailable")
	}
	// The native network worker must not inherit a protocol cancellation. Link
	// acquisition cancellation only until Connect has settled; Stop owns it after.
	networkCtx, cancel := context.WithCancel(context.Background())
	n.lease.push(func(cleanupCtx context.Context) error {
		disconnectErr := n.core.Disconnect(cleanupCtx)
		cancel()
		return disconnectErr
	})
	acquisitionDone := context.AfterFunc(ctx, cancel)
	err = connectContext(networkCtx, n.core)
	acquisitionDone()
	if err != nil {
		return n, err
	}
	if err := ctx.Err(); err != nil {
		return n, err
	}
	log.Infof(category, "network acquired session=%s network_generation=%d", ref.SessionID, ref.Generation)
	return n, nil
}

func (r *runtime) acquireNetworkTunnel(ctx context.Context, ref sessionapi.SessionRef, n *networkOwner) (TunnelLease, error) {
	if r.options.Tunnel == nil {
		return nil, errors.New("mobile network requires a TunnelProvider")
	}
	tun, err := r.options.Tunnel.Acquire(ctx, ref)
	if tun != nil {
		n.lease.push(tun.Release)
	}
	if err != nil {
		return nil, err
	}
	if tun == nil {
		return nil, errors.New("platform returned no network interface")
	}
	resolver, ok := r.options.Tunnel.(NetworkResolverProvider)
	if !ok {
		return nil, errors.New("platform underlying resolver is unavailable")
	}
	if err := resolver.ConfigureResolver(ctx, ref, n.cache); err != nil {
		return nil, err
	}
	return tun, nil
}

func (r *runtime) Start(ctx context.Context, ref sessionapi.SessionRef, profile sessionapi.RuntimeProfile) (sessionapi.RuntimeLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active {
		return nil, errors.New("another runtime lease is active")
	}
	if r.network == nil || r.network.ref.SessionID != ref.SessionID {
		return nil, errors.New("VPN network must be acquired before a protocol attempt")
	}
	r.active = true
	lease, err := r.startLocked(ctx, ref, profile)
	if err != nil {
		if lease != nil {
			// The native startup is still the sole owner of its TUN/device.
			// Keep r.active asserted until the transferred lease is stopped;
			// the manager will retain it in the generation ledger even though
			// Start returns the cancellation to its caller now.
			lease.setOnDone(r.releaseActive)
			return lease, err
		}
		var cleanupFailure *sessionapi.CleanupFailure
		if !errors.As(err, &cleanupFailure) {
			r.active = false
		}
		return nil, err
	}
	initialReadiness := r.options.InitialReadiness
	if lease.waitReady != nil {
		initialReadiness = func(checkCtx context.Context, checkRef sessionapi.SessionRef, proxyAddr string) error {
			if err := lease.waitReady(checkCtx); err != nil {
				return fmt.Errorf("wait for protocol transport readiness: %w", err)
			}
			return r.options.InitialReadiness(checkCtx, checkRef, proxyAddr)
		}
	}
	if err := waitForInitialReadiness(
		ctx,
		ref,
		lease.proxyAddr,
		initialReadiness,
		r.options.ReadinessAttempts,
		r.options.ReadinessAttemptTimeout,
		r.options.ReadinessRetryInterval,
	); err != nil {
		if ctx.Err() == context.Canceled && onlyCanceledLeaves(err) {
			log.Debugf(category, "initial readiness failed generation=%d; rolling back runtime lease: %v", ref.Generation, err)
		} else {
			log.Warnf(category, "initial readiness failed generation=%d; rolling back runtime lease: %v", ref.Generation, err)
		}
		cleanupErr := lease.Stop(sessionapi.CleanupContext(ctx))
		if cleanupErr == nil {
			r.active = false
		}
		if cleanupErr != nil {
			log.Errorf(category, "initial readiness rollback failed generation=%d: %v", ref.Generation, cleanupErr)
		} else {
			log.Debugf(category, "initial readiness rollback complete generation=%d", ref.Generation)
		}
		cause := errors.Join(fmt.Errorf("wait for initial tunnel readiness: %w", err), markCleanupFailure(cleanupErr))
		if cleanupErr != nil {
			lease.setOnDone(r.releaseActive)
			return lease, cause
		}
		return nil, cause
	}
	if err := r.network.core.Attach(lease.socketContext, ref.Generation, lease.proxyAddr, r.network.cache, lease.bypass); err != nil {
		cleanupErr := lease.Stop(sessionapi.CleanupContext(ctx))
		if cleanupErr == nil {
			r.active = false
			return nil, err
		}
		lease.setOnDone(r.releaseActive)
		return lease, errors.Join(err, markCleanupFailure(cleanupErr))
	}
	core := r.network.core
	lease.push(func(ctx context.Context) error { return core.Detach(ctx, ref.Generation) })
	lease.setOnDone(r.releaseActive)
	healthInterval, healthFailureThreshold := testHealthMonitorTiming(r.options.HealthInterval, r.options.HealthFailureThreshold)
	lease.startHealthMonitor(ctx, ref, lease.proxyAddr, r.options.ConnectedHealth, healthInterval, healthFailureThreshold)
	return lease, nil
}

func (r *runtime) releaseActive(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil {
		r.active = false
	}
}

func (r *runtime) startLocked(ctx context.Context, ref sessionapi.SessionRef, profile sessionapi.RuntimeProfile) (*lease, error) {
	if profile.Config.Empty() {
		return nil, errors.New("runtime profile has no normalized config")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	owned := &lease{}
	fail := func(cause error) (*lease, error) {
		cleanupErr := owned.Stop(sessionapi.CleanupContext(ctx))
		if cleanupErr != nil {
			return owned, errors.Join(cause, markCleanupFailure(cleanupErr))
		}
		return nil, cause
	}
	var bypass *tunnel.BypassPolicy
	var err error
	if _, native := r.options.Inputs.(defaultInputProvider); native {
		bypass, err = tunnel.ResolveBypassPolicyWithResolver(ctx, profile.ExcludeCIDRs, r.network.cache.ResolveAddresses)
	} else {
		bypass, err = r.options.Inputs.Resolve(ctx, profile.ExcludeCIDRs)
	}
	if err != nil {
		return fail(fmt.Errorf("resolve bypass policy: %w", err))
	}
	if bypass == nil {
		return fail(errors.New("bypass policy unavailable"))
	}
	n := r.network
	var protect SocketProtector
	if r.options.Tunnel != nil {
		protect = func(ctx context.Context, fd int) error {
			if contextErr := ctx.Err(); contextErr != nil {
				return contextErr
			}
			return r.options.Tunnel.ProtectSocket(ctx, n.ref, fd)
		}
	}
	ctx, scope := protected_dialer.NewSocketScope(ctx, ref.Generation, protect)
	owned.socketContext = ctx
	owned.push(scope.Close)
	device, err := r.options.NewDevice(ctx, ref, profile, protect, n.cache)
	if device != nil {
		owned.push(func(ctx context.Context) error {
			if closer, ok := device.(interface{ CloseContext(context.Context) error }); ok {
				return closer.CloseContext(ctx)
			}
			return device.Close()
		})
	}
	if err != nil {
		return fail(fmt.Errorf("create protocol device: %w", err))
	}
	if device == nil {
		return fail(errors.New("protocol device unavailable"))
	}
	// Retain the device-reported numeric upstream inventory in this
	// attempt's direct policy, including IPv6 answers.
	if upstreams, ok := device.(interface{ UpstreamIPs() []net.IP }); ok {
		bypass, err = bypass.WithNumericIPs(upstreams.UpstreamIPs())
		if err != nil {
			return fail(fmt.Errorf("extend bypass policy with numeric protocol upstreams: %w", err))
		}
	}
	owned.proxyAddr, owned.bypass = device.GetProxyAddr(), bypass
	if ready, ok := device.(interface{ WaitReady(context.Context) error }); ok {
		owned.waitReady = ready.WaitReady
	}
	if owned.proxyAddr == "" {
		return fail(errors.New("protocol device returned an empty SOCKS endpoint"))
	}
	mark, iface := n.core.ProtocolParameters()
	if err := device.Open(mark, iface); err != nil {
		return fail(fmt.Errorf("open protocol: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	log.Infof(category, "protocol opened generation=%d network_generation=%d protocol=%s", ref.Generation, n.ref.Generation, profile.Summary.Protocol)
	return owned, nil
}

// onlyCanceledLeaves excludes joined failures that include a genuine error.
func onlyCanceledLeaves(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		found := false
		for _, child := range joined.Unwrap() {
			if child == nil {
				continue
			}
			found = true
			if !onlyCanceledLeaves(child) {
				return false
			}
		}
		return found
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		if child := wrapped.Unwrap(); child != nil {
			return onlyCanceledLeaves(child)
		}
	}
	return errors.Is(err, context.Canceled)
}

func waitForInitialReadiness(
	ctx context.Context,
	ref sessionapi.SessionRef,
	proxyAddr string,
	check ConnectedHealthFunc,
	attempts int,
	attemptTimeout time.Duration,
	retryInterval time.Duration,
) error {
	var attemptErrors []error
	if attempts < 1 {
		return errors.New("initial readiness has no attempts configured")
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return errors.Join(err, errors.Join(attemptErrors...))
		}
		startedAt := time.Now()
		log.Debugf(category, "initial readiness attempt begin generation=%d attempt=%d/%d timeout=%s", ref.Generation, attempt, attempts, attemptTimeout)
		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		err := check(attemptCtx, ref, proxyAddr)
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
		log.Debugf(category, "initial readiness attempt failed generation=%d attempt=%d/%d outcome=%s elapsed=%s error=%v", ref.Generation, attempt, attempts, outcome, time.Since(startedAt).Truncate(time.Millisecond), err)
		attemptErrors = append(attemptErrors, fmt.Errorf("attempt %d/%d: %w", attempt, attempts, err))
		if ctxErr := ctx.Err(); ctxErr != nil {
			return errors.Join(ctxErr, errors.Join(attemptErrors...))
		}
		if attempt == attempts {
			break
		}
		timer := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return errors.Join(ctx.Err(), errors.Join(attemptErrors...))
		case <-timer.C:
		}
	}
	return fmt.Errorf("readiness failed after %d attempts: %w", attempts, errors.Join(attemptErrors...))
}

func connectContext(ctx context.Context, client sessionCore) error {
	result := make(chan error, 1)
	go func() { result <- client.Connect(ctx) }()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		var cancelErr error
		if canceler, ok := client.(connectCanceler); ok {
			// Native startup owns the lifecycle mutex while it is inside a
			// non-context-aware platform call.  Its cancellation request is
			// deliberately lock-free; cleanup remains serialized by Connect and
			// the rollback below after Connect has returned.
			canceler.CancelConnect()
			// Do not run the lease rollback from this stack while Connect is
			// still active.  startLocked transfers ownership to its caller;
			// the caller fences the generation and invokes Stop after Connect
			// has returned.
			return errors.Join(ctx.Err(), errConnectCancellationPending)
		} else {
			cancelErr = client.Disconnect(sessionapi.CleanupContext(ctx))
		}
		// Do not return a failed start while Connect can still publish a late
		// successful core. Waiting for its result preserves the ownership
		// ordering: dependent TUN/input resources are released only after the
		// native operation has stopped mutating them.
		connectErr := <-result
		return errors.Join(ctx.Err(), cancelErr, connectErr)
	}
}

type lease struct {
	socketContext context.Context
	bypass        *tunnel.BypassPolicy
	proxyAddr     string
	waitReady     func(context.Context) error
	stopMu        sync.Mutex
	stopped       bool
	undo          []func(context.Context) error
	onDone        func(error)
	healthCancel  context.CancelFunc
	healthDone    chan struct{}
	healthFailed  chan error
}

func (l *lease) push(fn func(context.Context) error) { l.undo = append(l.undo, fn) }
func (l *lease) setOnDone(fn func(error))            { l.onDone = fn }

// HealthFailures implements HealthMonitoringLease. It is closed after the
// lease has stopped, so the manager watcher cannot outlive its runtime lease.
func (l *lease) HealthFailures() <-chan error { return l.healthFailed }

func (l *lease) startHealthMonitor(parent context.Context, ref sessionapi.SessionRef, proxyAddr string, check ConnectedHealthFunc, interval time.Duration, threshold int) {
	ctx, cancel := context.WithCancel(parent)
	l.healthCancel = cancel
	l.healthDone = make(chan struct{})
	l.healthFailed = make(chan error, 1)
	go func() {
		defer close(l.healthDone)
		defer close(l.healthFailed)
		var failures []error
		for {
			if err := check(ctx, ref, proxyAddr); err != nil {
				failures = append(failures, err)
				if len(failures) >= threshold {
					cause := errors.Join(failures...)
					select {
					case l.healthFailed <- cause:
					case <-ctx.Done():
					}
					return
				}
			} else {
				failures = nil
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
	l.stopMu.Lock()
	defer l.stopMu.Unlock()
	if l.stopped {
		return nil
	}
	if l.healthCancel != nil {
		l.healthCancel()
	}
	if l.healthDone != nil {
		select {
		case <-l.healthDone:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	// Never release a dependency while the owner above it still has resources.
	// Failed entries stay in place so a later Stop can retry this exact owner.
	for len(l.undo) > 0 {
		i := len(l.undo) - 1
		if err := l.undo[i](ctx); err != nil {
			return err
		}
		l.undo = l.undo[:i]
	}
	l.stopped = true
	if l.onDone != nil {
		l.onDone(nil)
	}
	return nil
}

type defaultInputProvider struct{}

func (defaultInputProvider) Resolve(ctx context.Context, cidrs []string) (*tunnel.BypassPolicy, error) {
	policy, err := tunnel.ResolveBypassPolicy(ctx, cidrs)
	if err != nil {
		return nil, fmt.Errorf("resolve exclusion policy: %w", err)
	}
	return policy, nil
}

func unsupportedDevice(_ context.Context, _ sessionapi.SessionRef, _ sessionapi.RuntimeProfile, _ SocketProtector, _ *dnscache.Cache) (protocol.ProtocolDevice, error) {
	return nil, errors.New("native protocol device factory is not installed")
}

func measureConnectedHealth(ctx context.Context, proxyAddr string) error {
	timeout, err := connectedHealthTimeout(ctx)
	if err != nil {
		return err
	}
	_, probeErr := probe.MeasureTunnelProbeAverageLatencyMillisWithContext(ctx, int64(timeout/time.Millisecond), proxyAddr)
	return errors.Join(probeErr, ctx.Err())
}

func connectedHealthTimeout(ctx context.Context) (time.Duration, error) {
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

func defaultConnectedHealth(ctx context.Context, _ sessionapi.SessionRef, proxyAddr string) error {
	return measureConnectedHealth(ctx, proxyAddr)
}
