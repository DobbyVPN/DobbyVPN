// Package sessionapi is the transport-neutral API for a DobbyVPN session.
//
// It deliberately does not bind a protocol implementation. Desktop gRPC and
// mobile bindings can use the same manager while supplying their own Runtime
// and PlatformAdapter. State changes wake platform clients; snapshots carry
// authoritative control state and profile identity.
package sessionapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
)

// Protocol is intentionally a small stable vocabulary used by all bindings.
type Protocol string

const (
	ProtocolOutline     Protocol = "OUTLINE"
	ProtocolXray        Protocol = "XRAY"
	ProtocolTrustTunnel Protocol = "TRUST_TUNNEL"
)

type State string

const (
	StateIdle       State = "IDLE"
	StateConfigured State = "CONFIGURED"
	StateProbing    State = "PROBING"
	StatePreparing  State = "PREPARING"
	StateConnected  State = "CONNECTED"
	StateStopping   State = "STOPPING"
	StateFailed     State = "FAILED"
)

type FailureCode string

const (
	FailureInvalidArgument FailureCode = "INVALID_ARGUMENT"
	FailureNotFound        FailureCode = "NOT_FOUND"
	FailureConflict        FailureCode = "CONFLICT"
	FailureNotConfigured   FailureCode = "NOT_CONFIGURED"
	FailureStaleGeneration FailureCode = "STALE_GENERATION"
	FailureUnsupported     FailureCode = "UNSUPPORTED"
	FailureMalformedConfig FailureCode = "MALFORMED_CONFIG"
	FailureProbe           FailureCode = "PROBE_FAILED"
	FailurePlatform        FailureCode = "PLATFORM_FAILED"
	FailureRuntime         FailureCode = "RUNTIME_FAILED"
	FailureCanceled        FailureCode = "CANCELED"
	FailureInternal        FailureCode = "INTERNAL"
	FailureCleanup         FailureCode = "CLEANUP_FAILED"
)

// Error carries a stable caller-facing classification and message.
type Error struct {
	Code    FailureCode
	Message string
	Cause   error
}

func (e *Error) Error() string {
	message := string(e.Code) + ": " + e.Message
	if e.Cause != nil {
		message += ": " + e.Cause.Error()
	}
	return message
}

func (e *Error) Unwrap() error { return e.Cause }

func failure(code FailureCode, message string) error { return &Error{Code: code, Message: message} }

func failureWithCause(code FailureCode, message string, cause error) error {
	return &Error{Code: code, Message: message, Cause: cause}
}

func CodeOf(err error) FailureCode {
	var target *Error
	if errors.As(err, &target) {
		return target.Code
	}
	return FailureInternal
}

// ProfileSummary is the connection inventory returned by session status.
type ProfileSummary struct {
	Index       int32
	Protocol    Protocol
	Description string
}

type Warning struct {
	Code    string
	Message string
}

type ConfigureResult struct {
	Digest     string
	Sequence   uint64
	Profiles   []ProfileSummary
	Warnings   []Warning
	SourceKind ConfigSourceKind
}

type StartMode string

const (
	AutoSelect   StartMode = "AUTO_SELECT"
	ProfileIndex StartMode = "PROFILE_INDEX"
)

type StartTarget struct {
	Mode  StartMode
	Index int
}

type StartResult struct {
	Generation uint64
	Sequence   uint64
}

type StopResult struct {
	Generation uint64
	Sequence   uint64
}

// StateChange is a content-free native wake hint. Clients read Snapshot for
// the authoritative current state and revision.
type StateChange struct {
	SessionID  string
	Generation uint64
	State      State
	Failure    FailureCode
}

type SnapshotResult struct {
	SessionID          string
	Sequence           uint64
	Generation         uint64
	State              State
	Configured         bool
	Digest             string
	SourceKind         ConfigSourceKind
	SourceURL          string
	Profiles           []ProfileSummary
	Warnings           []Warning
	ActiveProfile      *ProfileSummary
	LastFailure        FailureCode
	LastFailureMessage string
	CleanupComplete    bool
	Recovering         bool
}

// SessionRef is passed to every platform/runtime operation.  It prevents a
// delayed callback from being accidentally applied to a subsequent attempt.
type SessionRef struct {
	SessionID  string
	Generation uint64
}

// RuntimeProfile is deliberately separate from ProfileSummary: it is only
// supplied inside the trusted process to the protocol runtime.
type RuntimeProfile struct {
	Summary          ProfileSummary
	NormalizedFormat ConfigFormat
	NormalizedConfig []byte
	// ExcludeCIDRs is interpreted once by Go and remains private to the
	// runtime. Platform shells must not parse routing inputs from the original
	// configuration.
	ExcludeCIDRs []string
}

// ConfigFormat identifies the representation a protocol runtime should use.
type ConfigFormat string

const (
	ConfigTOML         ConfigFormat = "TOML"
	ConfigJSON         ConfigFormat = "JSON"
	ConfigTransportURL ConfigFormat = "TRANSPORT_URL"
)

type ProbeResult struct{ LatencyMillis int64 }

// Runtime owns protocol process/device work. Implementations must honor ctx;
// cancellation is how Stop prevents a late completion from reconnecting.
type Runtime interface {
	Probe(context.Context, SessionRef, RuntimeProfile) (ProbeResult, error)
	Start(context.Context, SessionRef, RuntimeProfile) (RuntimeLease, error)
}

type RuntimeLease interface{ Stop(context.Context) error }

// HealthMonitoringLease is implemented by runtimes which own connected-health
// checks. The runtime applies its own failure threshold and sends at most one
// notification for a lease. Closing the channel means monitoring stopped.
//
// Runtimes without connected-health checks return a plain RuntimeLease.
type HealthMonitoringLease interface {
	RuntimeLease
	HealthFailures() <-chan struct{}
}

// PlatformAdapter owns only platform concerns.  PrepareTunnel must allocate a
// fresh TUN policy for every generation; it must not reuse a prior lease.
// ProtectSocket is exposed here so implementations can make failure fatal for
// non-loopback protocol dials.  All callbacks include SessionRef.
type PlatformAdapter interface {
	PrepareTunnel(context.Context, SessionRef) (PlatformLease, error)
	ProtectSocket(context.Context, SessionRef, int) error
	PublishState(context.Context, StateChange)
}

type PlatformLease interface{ Release(context.Context) error }

type ManagerOptions struct {
	Runtime  Runtime
	Platform PlatformAdapter
	Loader   ConfigLoader
	Now      func() time.Time
}

type Manager struct {
	runtime  Runtime
	platform PlatformAdapter
	loader   ConfigLoader
	now      func() time.Time
	session  *session
}

const (
	autoRecoveryLimit   = 3
	autoRecoveryStable  = 5 * time.Minute
	autoRecoveryMessage = "Automatic reconnect limit reached. Check your connection and connect again."
)

type session struct {
	mu sync.Mutex

	id         string
	state      State
	generation uint64
	configured bool
	digest     string
	sourceKind ConfigSourceKind
	sourceURL  string
	profiles   []RuntimeProfile
	warnings   []Warning

	active                     *ProfileSummary
	lastFailure                FailureCode
	lastFailureMessage         string
	cleanupDone                bool
	cleanupFailed              bool
	cancel                     context.CancelFunc
	ledger                     *ledger
	workerDone                 chan struct{}
	activeTarget               StartTarget
	restartAfterCleanup        bool
	failureAfterCleanup        FailureCode
	failureMessageAfterCleanup string
	recovering                 bool
	recoveryOriginGeneration   uint64
	recoveryCount              int
	lastConnectedAt            time.Time
	hasConnectedAt             bool
	sequence                   uint64
	watchers                   map[*snapshotWatcher]struct{}
}

type snapshotWatcher struct {
	updates chan SnapshotResult
	done    chan struct{}
	once    sync.Once
}

// NewManager never starts a real core.  Its default runtime fails with the
// typed UNSUPPORTED result until a binding injects an implementation.
func NewManager(options ManagerOptions) *Manager {
	r := options.Runtime
	if r == nil {
		r = unsupportedRuntime{}
	}
	p := options.Platform
	if p == nil {
		p = noopPlatform{}
	}
	loader := options.Loader
	if loader == nil {
		loader = DefaultConfigLoader{}
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	id := randomID()
	return &Manager{
		runtime: r, platform: p, loader: loader, now: now,
		session: &session{
			id: id, state: StateIdle, cleanupDone: true, sequence: 1,
			watchers: make(map[*snapshotWatcher]struct{}),
		},
	}
}

// Watch delivers the current snapshot immediately and then the latest snapshot
// after each state change. Slow readers receive the newest state, not history.
func (m *Manager) Watch(ctx context.Context, sessionID string) (updates <-chan SnapshotResult, closeSubscription func(), err error) {
	s, err := m.get(sessionID)
	if err != nil {
		return nil, func() {}, err
	}
	watcher := &snapshotWatcher{updates: make(chan SnapshotResult, 1), done: make(chan struct{})}
	s.mu.Lock()
	watcher.updates <- snapshotLocked(s)
	s.watchers[watcher] = struct{}{}
	s.mu.Unlock()
	closeSubscription = func() { m.closeWatcher(s, watcher) }
	go func() {
		select {
		case <-ctx.Done():
			closeSubscription()
		case <-watcher.done:
		}
	}()
	return watcher.updates, closeSubscription, nil
}

func (m *Manager) closeWatcher(s *session, watcher *snapshotWatcher) {
	watcher.once.Do(func() {
		s.mu.Lock()
		delete(s.watchers, watcher)
		close(watcher.done)
		close(watcher.updates)
		s.mu.Unlock()
	})
}

func (m *Manager) ValidateConfig(ctx context.Context, rawConfig []byte) (ConfigureResult, error) {
	loaded, parsed, err := m.loadConfig(ctx, rawConfig)
	if err != nil {
		return ConfigureResult{}, err
	}
	return ConfigureResult{Digest: parsed.digest, Profiles: summaries(parsed.profiles), Warnings: cloneWarnings(parsed.warnings), SourceKind: loaded.Kind}, nil
}

func (m *Manager) loadConfig(ctx context.Context, rawConfig []byte) (LoadedConfig, parsedConfig, error) {
	loaded, err := m.loader.Load(ctx, rawConfig)
	if err != nil {
		return LoadedConfig{}, parsedConfig{}, safeConfigError(rawConfig, err)
	}
	parsed, err := parseConfig(loaded.Raw)
	if err != nil {
		var domain *Error
		if errors.As(err, &domain) {
			if domain.Cause != nil {
				return LoadedConfig{}, parsedConfig{}, failure(domain.Code, domain.Message)
			}
			return LoadedConfig{}, parsedConfig{}, err
		}
		return LoadedConfig{}, parsedConfig{}, failure(FailureMalformedConfig, "configuration is malformed")
	}
	return loaded, parsed, nil
}

func safeConfigError(raw []byte, err error) error {
	if errors.Is(err, context.Canceled) {
		return failure(FailureCanceled, "configuration loading was canceled")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return failure(FailureCanceled, "configuration loading timed out")
	}
	var domain *Error
	if errors.As(err, &domain) {
		if domain.Cause != nil {
			return failure(domain.Code, domain.Message)
		}
		return err
	}
	if sourceSchemeRE.MatchString(strings.TrimSpace(string(raw))) {
		return failure(FailureInvalidArgument, "configuration URL could not be fetched")
	}
	return failure(FailureInvalidArgument, "configuration could not be loaded")
}

func (m *Manager) Configure(ctx context.Context, sessionID string, expectedSequence uint64, rawConfig []byte) (ConfigureResult, error) {
	s, err := m.get(sessionID)
	if err != nil {
		return ConfigureResult{}, err
	}
	s.mu.Lock()
	preloadErr := validateConfigureBeforeLoad(ctx, s, expectedSequence)
	s.mu.Unlock()
	if preloadErr != nil {
		return ConfigureResult{}, preloadErr
	}

	loaded, parsed, loadErr := m.loadConfig(ctx, rawConfig)
	if loadErr != nil {
		return ConfigureResult{}, loadErr
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if acceptErr := validateConfigureBeforeAccept(ctx, s, expectedSequence); acceptErr != nil {
		return ConfigureResult{}, acceptErr
	}
	s.profiles, s.digest, s.sourceKind, s.sourceURL, s.warnings, s.configured = parsed.profiles, parsed.digest, loaded.Kind, loaded.SourceURL, parsed.warnings, true
	s.active, s.lastFailure, s.lastFailureMessage, s.state, s.cleanupDone, s.cleanupFailed = nil, "", "", StateConfigured, true, false
	s.recovering, s.recoveryOriginGeneration, s.recoveryCount = false, 0, 0
	m.appendLocked(s)
	result := ConfigureResult{Digest: s.digest, Sequence: s.sequence, Profiles: summaries(s.profiles), Warnings: cloneWarnings(s.warnings), SourceKind: s.sourceKind}
	return cloneConfigure(result), nil
}

func validateConfigureBeforeLoad(ctx context.Context, s *session, expectedSequence uint64) error {
	if s.sequence != expectedSequence {
		return failure(FailureConflict, "session changed; refresh its snapshot before configuring")
	}
	if configurationBlocked(s) {
		return failure(FailureConflict, "cannot configure until the previous generation cleaned up successfully")
	}
	if err := ctx.Err(); err != nil {
		return failureWithCause(FailureCanceled, "configuration was canceled before loading", err)
	}
	return nil
}

func validateConfigureBeforeAccept(ctx context.Context, s *session, expectedSequence uint64) error {
	if s.sequence != expectedSequence {
		return failure(FailureConflict, "session changed while configuration was loading; refresh its snapshot")
	}
	if err := ctx.Err(); err != nil {
		return failureWithCause(FailureCanceled, "configuration was canceled before it was accepted", err)
	}
	if configurationBlocked(s) {
		return failure(FailureConflict, "cannot configure until the previous generation cleaned up successfully")
	}
	return nil
}

func configurationBlocked(s *session) bool {
	return s.state == StateProbing || s.state == StatePreparing || s.state == StateConnected || s.state == StateStopping || s.recovering || !s.cleanupDone || s.cleanupFailed
}

func (m *Manager) Start(requestCtx context.Context, sessionID string, expectedSequence uint64, target StartTarget) (result StartResult, err error) {
	s, err := m.get(sessionID)
	if err != nil {
		return StartResult{}, err
	}
	if err := requestCtx.Err(); err != nil {
		return StartResult{}, failureWithCause(FailureCanceled, "start was canceled before it was accepted", err)
	}
	s.mu.Lock()
	if s.sequence != expectedSequence {
		s.mu.Unlock()
		return StartResult{}, failure(FailureConflict, "session changed; refresh its snapshot before starting")
	}
	if !s.configured {
		s.mu.Unlock()
		return StartResult{}, failure(FailureNotConfigured, "configure a session before starting it")
	}
	if s.state == StateProbing || s.state == StatePreparing || s.state == StateConnected || s.state == StateStopping || s.recovering || !s.cleanupDone || s.cleanupFailed {
		s.mu.Unlock()
		return StartResult{}, failure(FailureConflict, "previous generation has not completed cleanup")
	}
	if target.Mode != AutoSelect && target.Mode != ProfileIndex {
		s.mu.Unlock()
		return StartResult{}, failure(FailureInvalidArgument, "start mode must be AUTO_SELECT or PROFILE_INDEX")
	}
	if target.Mode == ProfileIndex && (target.Index < 0 || target.Index >= len(s.profiles)) {
		s.mu.Unlock()
		return StartResult{}, failure(FailureInvalidArgument, "profile index is out of range")
	}
	s.generation++
	generation := s.generation
	ctx, cancel := context.WithCancel(context.WithoutCancel(requestCtx))
	s.cancel, s.ledger, s.workerDone, s.cleanupDone, s.cleanupFailed, s.active, s.lastFailure, s.lastFailureMessage = cancel, &ledger{}, make(chan struct{}), false, false, nil, "", ""
	s.activeTarget, s.restartAfterCleanup, s.failureAfterCleanup = target, false, ""
	s.failureMessageAfterCleanup = ""
	s.recovering, s.recoveryOriginGeneration, s.recoveryCount = false, 0, 0
	s.lastConnectedAt = time.Time{}
	s.hasConnectedAt = false
	s.state = StateProbing
	m.appendLocked(s)
	result = StartResult{Generation: generation, Sequence: s.sequence}
	s.mu.Unlock()
	go m.runStart(ctx, s, generation, target) // #nosec G118 -- generation work intentionally outlives the initiating request and owns its cancel function.
	return result, nil
}

func (m *Manager) Stop(_ context.Context, sessionID string, generation uint64) (result StopResult, err error) {
	s, err := m.get(sessionID)
	if err != nil {
		return StopResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation == 0 {
		err := failure(FailureStaleGeneration, "generation is not active for this session")
		return StopResult{}, err
	}
	if generation != s.generation {
		// The UI can issue Stop from a recovery snapshot whose cleanup-complete
		// IDLE was published just before the next generation was reserved.
		// Accept that originating generation only while this recovery chain is
		// still active, then stop the current generation under the same lock.
		if !s.recovering || s.recoveryOriginGeneration != generation {
			err := failure(FailureStaleGeneration, "generation is not active for this session")
			return StopResult{}, err
		}
		generation = s.generation
	}
	if s.state == StateIdle || s.state == StateConfigured || s.state == StateFailed {
		// A runtime-owned health failure can finish cleanup before the mobile
		// caller gets to issue its ordinary stop request. Once this exact
		// generation is fully cleaned, stop is an idempotent acknowledgement;
		// it must not turn a clean terminal state into a misleading stale-stop
		// failure. Cleanup failures remain errors and still block restart.
		if s.generation == generation && s.cleanupDone && !s.cleanupFailed && s.state != StateConfigured {
			if s.recovering || s.recoveryOriginGeneration != 0 {
				s.recovering, s.restartAfterCleanup, s.recoveryOriginGeneration = false, false, 0
				m.appendLocked(s)
			}
			result = StopResult{Generation: generation, Sequence: s.sequence}
			return result, nil
		}
		err := failure(FailureStaleGeneration, "generation is no longer active")
		return StopResult{}, err
	}
	result = StopResult{Generation: generation, Sequence: s.sequence}
	if s.recovering || s.recoveryOriginGeneration != 0 {
		// A Stop during health-triggered teardown cancels the queued retry even
		// though the cleanup worker already owns the STOPPING transition.
		s.recovering, s.restartAfterCleanup = false, false
		s.recoveryOriginGeneration = 0
	}
	if s.state != StateStopping {
		s.state = StateStopping
		m.appendLocked(s)
		result.Sequence = s.sequence
		if s.cancel != nil {
			s.cancel()
		}
		// The start worker owns every acquisition until it exits.  A runtime that
		// ignores cancellation must keep this generation STOPPING, rather than
		// allowing a new TUN/runtime to overlap it.
		done := s.workerDone
		go func() { <-done; m.finishAfterStop(s, generation, failure(FailureCanceled, "stop requested")) }()
	}
	return result, nil
}

// ProtectSocket is the session API path for platform socket protection.
// Runtimes normally receive this as a closure from their binding before each
// non-loopback protocol dial. A protection failure aborts that dial.
func (m *Manager) ProtectSocket(ctx context.Context, ref SessionRef, fd int, loopback bool) (err error) {
	if fd < 0 {
		return failure(FailureInvalidArgument, "socket descriptor must be non-negative")
	}
	s, err := m.get(ref.SessionID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	valid := s.generation == ref.Generation && (s.state == StateProbing || s.state == StatePreparing || s.state == StateConnected)
	s.mu.Unlock()
	if !valid {
		return failure(FailureStaleGeneration, "socket protection belongs to a stale generation")
	}
	if loopback {
		return nil
	}
	if err := m.platform.ProtectSocket(ctx, ref, fd); err != nil {
		return wrapFailure(FailurePlatform, err)
	}
	return nil
}

// reportHealthFailure cleans up a failed connected generation before
// AUTO_SELECT failover. PROFILE_INDEX fails without changing profile identity.
func (m *Manager) reportHealthFailure(s *session, generation uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation == 0 || generation != s.generation || s.state != StateConnected {
		return
	}
	m.prepareHealthRecoveryLocked(s, generation)
	s.state = StateStopping
	m.appendLocked(s)
	if s.cancel != nil {
		s.cancel()
	}
	done := s.workerDone
	go func() {
		<-done
		m.finishAfterStop(s, generation, failure(FailureCanceled, "health check requested failover"))
	}()
}

func (m *Manager) prepareHealthRecoveryLocked(s *session, generation uint64) {
	if s.activeTarget.Mode != AutoSelect {
		s.failureAfterCleanup = FailureRuntime
		s.recovering = false
		s.recoveryOriginGeneration = 0
		return
	}
	if s.hasConnectedAt && m.now().Sub(s.lastConnectedAt) >= autoRecoveryStable {
		s.recoveryCount = 0
		s.recoveryOriginGeneration = 0
	}
	if s.recoveryCount < autoRecoveryLimit {
		s.recoveryCount++
		s.restartAfterCleanup = true
		s.recovering = true
		if s.recoveryOriginGeneration == 0 {
			s.recoveryOriginGeneration = generation
		}
		return
	}
	s.failureAfterCleanup = FailureRuntime
	s.failureMessageAfterCleanup = autoRecoveryMessage
	s.recovering = false
	s.recoveryOriginGeneration = 0
}

func (m *Manager) Snapshot(_ context.Context, sessionID string) (result SnapshotResult, err error) {
	s, err := m.get(sessionID)
	if err != nil {
		return SnapshotResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result = snapshotLocked(s)
	return result, nil
}

func (m *Manager) Reset(_ context.Context, sessionID string, expectedSequence uint64) (SnapshotResult, error) {
	s, err := m.get(sessionID)
	if err != nil {
		return SnapshotResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sequence != expectedSequence {
		return SnapshotResult{}, failure(FailureConflict, "session changed; refresh its snapshot before resetting")
	}
	if !s.cleanupDone || s.cleanupFailed || s.recovering || s.state == StateProbing || s.state == StatePreparing || s.state == StateConnected || s.state == StateStopping {
		return SnapshotResult{}, failure(FailureConflict, "successful cleanup is required before resetting")
	}
	s.configured, s.digest, s.sourceKind, s.sourceURL = false, "", "", ""
	s.profiles, s.warnings, s.active = nil, nil, nil
	s.lastFailure, s.lastFailureMessage, s.state = "", "", StateIdle
	s.recovering, s.recoveryOriginGeneration, s.recoveryCount = false, 0, 0
	s.restartAfterCleanup, s.failureAfterCleanup, s.failureMessageAfterCleanup = false, "", ""
	s.lastConnectedAt = time.Time{}
	s.hasConnectedAt = false
	s.cleanupDone, s.cleanupFailed = true, false
	m.appendLocked(s)
	return snapshotLocked(s), nil
}

// runStart is one lifecycle transaction whose branches all preserve generation
// fencing and late-lease cleanup; keeping those checks together is deliberate.
//
//nolint:gocyclo,nestif // Refactoring the transaction would risk cleanup races.
func (m *Manager) runStart(ctx context.Context, s *session, generation uint64, target StartTarget) {
	defer m.signalWorkerDone(s, generation)
	profile, err := m.selectProfile(ctx, s, generation, target)
	if err != nil {
		m.finish(s, generation, err)
		return
	}
	if !m.advance(s, generation, StatePreparing, &profile.Summary) {
		return
	}
	platformLease, err := m.platform.PrepareTunnel(ctx, SessionRef{s.id, generation})
	if err == nil && platformLease == nil {
		err = failure(FailurePlatform, "platform returned an empty tunnel lease")
	}
	if err != nil {
		if platformLease != nil {
			s.mu.Lock()
			if s.generation == generation && s.ledger != nil && (s.state == StatePreparing || s.state == StateStopping) {
				s.ledger.push(func(c context.Context) error { return platformLease.Release(c) })
				stopping := s.state == StateStopping
				s.mu.Unlock()
				if stopping {
					return
				}
			} else {
				s.mu.Unlock()
				err = errors.Join(err, platformLease.Release(context.Background()))
			}
		}
		m.finish(s, generation, wrapFailure(FailurePlatform, err))
		return
	}
	s.mu.Lock()
	if s.generation == generation && s.state == StateStopping && s.ledger != nil {
		// Stop waits for this worker before draining the ledger. Retaining a
		// lease that arrived after cancellation makes its cleanup result part of
		// the same generation instead of silently discarding a late failure.
		s.ledger.push(func(c context.Context) error { return platformLease.Release(c) })
		s.mu.Unlock()
		return
	}
	if s.generation != generation || s.state != StatePreparing {
		s.mu.Unlock()
		if releaseErr := platformLease.Release(context.Background()); releaseErr != nil {
			m.finish(s, generation, wrapFailure(FailureCleanup, releaseErr))
		}
		return
	}
	s.ledger.push(func(c context.Context) error { return platformLease.Release(c) })
	s.mu.Unlock()
	runtimeLease, err := m.runtime.Start(ctx, SessionRef{s.id, generation}, profile)
	if err == nil && runtimeLease == nil {
		err = failure(FailureRuntime, "runtime returned an empty lease")
	}
	if err != nil {
		if runtimeLease != nil {
			s.mu.Lock()
			if s.generation == generation && s.ledger != nil && (s.state == StatePreparing || s.state == StateStopping) {
				s.ledger.push(func(c context.Context) error { return runtimeLease.Stop(c) })
				stopping := s.state == StateStopping
				s.mu.Unlock()
				if stopping {
					return
				}
			} else {
				s.mu.Unlock()
				err = errors.Join(err, runtimeLease.Stop(context.Background()))
			}
		}
		m.finish(s, generation, wrapFailure(FailureRuntime, err))
		return
	}
	s.mu.Lock()
	if s.generation == generation && s.state == StateStopping && s.ledger != nil {
		// A non-cooperative runtime may finish Start after cancellation. Stop's
		// waiter owns the ledger until this worker exits, so retain the lease and
		// report any Stop error through the normal cleanup failure contract.
		s.ledger.push(func(c context.Context) error { return runtimeLease.Stop(c) })
		s.mu.Unlock()
		return
	}
	if s.generation != generation || s.state != StatePreparing {
		s.mu.Unlock()
		if stopErr := runtimeLease.Stop(context.Background()); stopErr != nil {
			m.finish(s, generation, wrapFailure(FailureCleanup, stopErr))
		}
		return
	}
	s.ledger.push(func(c context.Context) error { return runtimeLease.Stop(c) })
	s.mu.Unlock()
	if !m.advance(s, generation, StateConnected, &profile.Summary) {
		return
	}
	if monitored, ok := runtimeLease.(HealthMonitoringLease); ok {
		go m.watchRuntimeHealth(s, generation, monitored)
	}
}

// watchRuntimeHealth receives one notification after the runtime's local threshold.
func (m *Manager) watchRuntimeHealth(s *session, generation uint64, lease HealthMonitoringLease) {
	failures := lease.HealthFailures()
	if failures == nil {
		return
	}
	for range failures {
		m.reportHealthFailure(s, generation)
		return
	}
}

func (m *Manager) signalWorkerDone(s *session, generation uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation == generation && s.workerDone != nil {
		close(s.workerDone)
	}
}

func (m *Manager) selectProfile(ctx context.Context, s *session, generation uint64, target StartTarget) (RuntimeProfile, error) {
	s.mu.Lock()
	profiles := append([]RuntimeProfile(nil), s.profiles...)
	s.mu.Unlock()
	if target.Mode == ProfileIndex {
		return profiles[target.Index], nil
	}
	type candidate struct {
		profile RuntimeProfile
		latency int64
	}
	var best *candidate
	for _, profile := range profiles {
		if ctx.Err() != nil {
			return RuntimeProfile{}, failure(FailureCanceled, "start was canceled")
		}
		ref := SessionRef{s.id, generation}
		// A probe is a complete, isolated tunnel attempt. In particular, mobile
		// socket protection is only valid while its platform lease is active.
		platformLease, prepareErr := m.platform.PrepareTunnel(ctx, ref)
		if prepareErr == nil && platformLease == nil {
			prepareErr = failure(FailurePlatform, "platform returned an empty tunnel lease for probe")
		}
		if prepareErr != nil {
			return RuntimeProfile{}, wrapFailure(FailurePlatform, prepareErr)
		}
		result, probeErr := m.runtime.Probe(ctx, ref, profile)
		releaseErr := platformLease.Release(context.Background())
		if releaseErr != nil {
			return RuntimeProfile{}, wrapFailure(FailurePlatform, releaseErr)
		}
		err := probeErr
		if err != nil {
			continue
		}
		if result.LatencyMillis < 0 {
			continue
		}
		if best == nil || result.LatencyMillis < best.latency || (result.LatencyMillis == best.latency && profile.Summary.Index < best.profile.Summary.Index) {
			item := candidate{profile, result.LatencyMillis}
			best = &item
		}
	}
	if best == nil {
		if ctx.Err() != nil {
			return RuntimeProfile{}, failure(FailureCanceled, "start was canceled")
		}
		return RuntimeProfile{}, failure(FailureProbe, "no configured profile passed its probe")
	}
	return best.profile, nil
}

func (m *Manager) advance(s *session, generation uint64, state State, profile *ProfileSummary) bool {
	s.mu.Lock()
	if s.generation != generation || s.state == StateStopping {
		s.mu.Unlock()
		return false
	}
	s.state, s.active = state, cloneSummaryPtr(profile)
	if state == StateConnected {
		s.lastConnectedAt = m.now()
		s.hasConnectedAt = true
		s.recovering = false
		s.recoveryOriginGeneration = 0
	}
	m.appendLocked(s)
	s.mu.Unlock()
	return true
}

func (m *Manager) finish(s *session, generation uint64, cause error) {
	m.finishWithPolicy(s, generation, cause, false)
}

// finishAfterStop is only called after workerDone closes. It is the sole path
// allowed to drain a STOPPING generation, which prevents a late noncooperative
// Runtime.Start from overlapping a newer generation.
func (m *Manager) finishAfterStop(s *session, generation uint64, cause error) {
	m.finishWithPolicy(s, generation, cause, true)
}

func (m *Manager) finishWithPolicy(s *session, generation uint64, cause error, allowStopping bool) {
	s.mu.Lock()
	if s.generation != generation || (s.state != StateProbing && s.state != StatePreparing && s.state != StateConnected && s.state != StateStopping) {
		s.mu.Unlock()
		return
	}
	if s.state == StateStopping && !allowStopping {
		s.mu.Unlock()
		return
	}
	wasStopping := s.state == StateStopping
	work := s.ledger
	s.ledger = nil
	s.mu.Unlock()
	var cleanupErr error
	if work != nil {
		cleanupErr = work.release(context.Background())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation != generation {
		return
	}
	s.cleanupDone, s.cleanupFailed, s.cancel = true, cleanupErr != nil, nil
	if cleanupErr != nil {
		s.restartAfterCleanup, s.failureAfterCleanup = false, ""
		s.failureMessageAfterCleanup = ""
		s.recovering, s.recoveryOriginGeneration = false, 0
		s.state, s.active, s.lastFailure = StateFailed, nil, FailureCleanup
		diagnostic := errors.Join(cause, cleanupErr)
		message := errorMessage(diagnostic)
		s.lastFailureMessage = message
		m.appendLocked(s)
		return
	}
	if wasStopping || cause != nil && CodeOf(cause) == FailureCanceled {
		restart := s.restartAfterCleanup
		terminalFailure := s.failureAfterCleanup
		s.restartAfterCleanup, s.failureAfterCleanup = false, ""
		if terminalFailure != "" {
			s.state, s.active, s.lastFailure, s.lastFailureMessage = StateFailed, nil, terminalFailure, s.failureMessageAfterCleanup
			s.failureMessageAfterCleanup = ""
			s.recovering, s.recoveryOriginGeneration = false, 0
			m.appendLocked(s)
			return
		}
		s.state, s.active, s.lastFailure, s.lastFailureMessage = StateIdle, nil, "", ""
		m.appendLocked(s)
		if restart {
			go m.startFailover(s, generation)
		} else {
			s.recovering, s.recoveryOriginGeneration = false, 0
		}
		return
	}
	message := errorMessage(cause)
	s.state, s.active, s.lastFailure, s.lastFailureMessage = StateFailed, nil, CodeOf(cause), message
	s.recovering, s.recoveryOriginGeneration = false, 0
	m.appendLocked(s)
}

func (m *Manager) startFailover(s *session, expectedGeneration uint64) {
	s.mu.Lock()
	if !s.configured || !s.cleanupDone || s.state != StateIdle || !s.recovering || s.generation != expectedGeneration {
		s.mu.Unlock()
		return
	}
	s.generation++
	generation := s.generation
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel, s.ledger, s.workerDone, s.cleanupDone, s.cleanupFailed, s.active, s.lastFailure, s.lastFailureMessage = cancel, &ledger{}, make(chan struct{}), false, false, nil, "", ""
	s.activeTarget, s.restartAfterCleanup, s.failureAfterCleanup = StartTarget{Mode: AutoSelect}, false, ""
	s.failureMessageAfterCleanup = ""
	s.state = StateProbing
	m.appendLocked(s)
	s.mu.Unlock()
	go m.runStart(ctx, s, generation, StartTarget{Mode: AutoSelect})
}

func (m *Manager) appendLocked(s *session) {
	s.sequence++
	m.platform.PublishState(context.Background(), StateChange{
		SessionID: s.id, Generation: s.generation, State: s.state, Failure: s.lastFailure,
	})
	current := snapshotLocked(s)
	for watcher := range s.watchers {
		select {
		case watcher.updates <- current:
		default:
			// A watcher receives current state, not a transition history.
			<-watcher.updates
			watcher.updates <- current
		}
	}
}

func (m *Manager) get(id string) (*session, error) {
	s := m.session
	if s == nil || s.id == "" {
		return nil, failure(FailureInternal, "could not allocate a session ID")
	}
	if id != "" && id != s.id {
		return nil, failure(FailureNotFound, "session owner has restarted")
	}
	return s, nil
}
func snapshotLocked(s *session) SnapshotResult {
	return SnapshotResult{
		SessionID: s.id, Sequence: s.sequence, Generation: s.generation, State: s.state,
		Configured: s.configured, Digest: s.digest, SourceKind: s.sourceKind, SourceURL: s.sourceURL,
		Profiles: summaries(s.profiles), Warnings: cloneWarnings(s.warnings),
		ActiveProfile: cloneSummaryPtr(s.active), LastFailure: s.lastFailure,
		LastFailureMessage: s.lastFailureMessage, CleanupComplete: s.cleanupDone,
		Recovering: s.recovering,
	}
}
func summaries(in []RuntimeProfile) []ProfileSummary {
	out := make([]ProfileSummary, len(in))
	for i := range in {
		out[i] = in[i].Summary
	}
	return out
}
func cloneConfigure(in ConfigureResult) ConfigureResult {
	return ConfigureResult{Digest: in.Digest, Sequence: in.Sequence, Profiles: append([]ProfileSummary(nil), in.Profiles...), Warnings: cloneWarnings(in.Warnings), SourceKind: in.SourceKind}
}
func cloneWarnings(in []Warning) []Warning { return append([]Warning(nil), in...) }
func cloneSummaryPtr(in *ProfileSummary) *ProfileSummary {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func wrapFailure(code FailureCode, err error) error {
	if err == nil {
		return nil
	}
	var domain *Error
	if errors.As(err, &domain) {
		return err
	}
	return failureWithCause(code, "operation failed", err)
}

func errorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func randomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

type ledger struct{ closers []func(context.Context) error }

func (l *ledger) push(closer func(context.Context) error) { l.closers = append(l.closers, closer) }
func (l *ledger) release(ctx context.Context) error {
	var errs []error
	for i := len(l.closers) - 1; i >= 0; i-- {
		if err := l.closers[i](ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

type unsupportedRuntime struct{}

func (unsupportedRuntime) Probe(context.Context, SessionRef, RuntimeProfile) (ProbeResult, error) {
	return ProbeResult{}, failure(FailureUnsupported, "no runtime is installed")
}
func (unsupportedRuntime) Start(context.Context, SessionRef, RuntimeProfile) (RuntimeLease, error) {
	return nil, failure(FailureUnsupported, "no runtime is installed")
}

type noopPlatform struct{}

func (noopPlatform) PrepareTunnel(context.Context, SessionRef) (PlatformLease, error) {
	return noopLease{}, nil
}
func (noopPlatform) ProtectSocket(context.Context, SessionRef, int) error { return nil }
func (noopPlatform) PublishState(context.Context, StateChange)            {}

type noopLease struct{}

func (noopLease) Release(context.Context) error { return nil }
