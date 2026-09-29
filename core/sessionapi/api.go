// Package sessionapi is the transport-neutral API for a DobbyVPN session.
//
// It keeps protocol parsing and session lifecycle independent from native UI
// and platform VPN APIs.
package sessionapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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

// CleanupFailure marks a failed rollback. Callers must not start another
// attempt while resources from the previous one may still be owned.
type CleanupFailure struct{ Err error }

func (e *CleanupFailure) Error() string { return "cleanup failed: " + e.Err.Error() }
func (e *CleanupFailure) Unwrap() error { return e.Err }

func failure(code FailureCode, message string) error { return &Error{Code: code, Message: message} }

func failureWithCause(code FailureCode, message string, cause error) error {
	return &Error{Code: code, Message: message, Cause: cause}
}

func CodeOf(err error) FailureCode {
	var cleanup *CleanupFailure
	if errors.As(err, &cleanup) {
		return FailureCleanup
	}
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

type ConfigureResult struct {
	Digest     string
	Sequence   uint64
	Profiles   []ProfileSummary
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
	// Nil reuses the accepted configuration; non-nil replaces it before AUTO_SELECT startup.
	Source []byte
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
	SourceError        string
	Profiles           []ProfileSummary
	ActiveProfile      *ProfileSummary
	LastFailure        FailureCode
	LastFailureMessage string
	CleanupComplete    bool
	Recovering         bool
	PrimaryAction      string
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
	NormalizedConfig []byte
	// ExcludeCIDRs is interpreted once by Go and remains private to the
	// runtime. Platform shells must not parse routing inputs from the original
	// configuration.
	ExcludeCIDRs []string
}

// Runtime owns protocol process/device work. Implementations must honor ctx;
// cancellation is how Stop prevents a late completion from reconnecting.
type Runtime interface {
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
	HealthFailures() <-chan error
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
	Runtime     Runtime
	Platform    PlatformAdapter
	Loader      ConfigLoader
	SourceStore SourceStore
	Now         func() time.Time
}

type Manager struct {
	runtime     Runtime
	platform    PlatformAdapter
	loader      ConfigLoader
	sourceStore SourceStore
	now         func() time.Time
	initErr     error
	session     *session
}

const (
	autoRecoveryLimit   = 3
	autoRecoveryStable  = 5 * time.Minute
	autoRecoveryMessage = "Automatic reconnect limit reached. Check your connection and connect again."
)

type session struct {
	mu sync.Mutex

	id          string
	state       State
	generation  uint64
	configured  bool
	digest      string
	sourceKind  ConfigSourceKind
	sourceURL   string
	sourceError string
	profiles    []RuntimeProfile

	active                     *ProfileSummary
	lastFailure                FailureCode
	lastFailureMessage         string
	cleanupDone                bool
	cleanupFailed              bool
	cancel                     context.CancelFunc
	ledger                     *ledger
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
	id, initErr := randomID()
	sourceURL := ""
	sourceKind := ConfigSourceKind("")
	sourceError := ""
	if options.SourceStore != nil {
		saved, err := options.SourceStore.Load(context.Background())
		if err != nil {
			sourceError = fmt.Sprintf("saved configuration URL could not be read: %v", err)
		} else if len(saved) > 0 {
			sourceURL = strings.TrimSpace(string(saved))
			if sourceURL != "" {
				sourceKind = ConfigSourceURL
			}
		}
	}
	return &Manager{
		runtime: r, platform: p, loader: loader, sourceStore: options.SourceStore, now: now, initErr: initErr,
		session: &session{
			id: id, state: StateIdle, cleanupDone: true, sequence: 1, sourceURL: sourceURL,
			sourceKind: sourceKind, sourceError: sourceError,
		},
	}
}

// AttachSourceStore installs the native storage adapter after a mobile OS
// bridge has registered. Desktop backends pass their store at construction.
func (m *Manager) AttachSourceStore(ctx context.Context, store SourceStore) error {
	if store == nil {
		return failure(FailureInvalidArgument, "source storage is unavailable")
	}
	s := m.session
	s.mu.Lock()
	if m.sourceStore != nil {
		s.mu.Unlock()
		return nil
	}
	m.sourceStore = store
	s.mu.Unlock()

	saved, err := store.Load(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.sourceError = fmt.Sprintf("saved configuration URL could not be read: %v", err)
	} else if !s.configured && s.sourceURL == "" && len(saved) > 0 {
		s.sourceURL = strings.TrimSpace(string(saved))
		if s.sourceURL != "" {
			s.sourceKind = ConfigSourceURL
		}
		s.sourceError = ""
	}
	m.appendLocked(s)
	return nil
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
			return LoadedConfig{}, parsedConfig{}, err
		}
		return LoadedConfig{}, parsedConfig{}, failureWithCause(FailureMalformedConfig, "configuration is malformed", err)
	}
	return loaded, parsed, nil
}

func safeConfigError(raw []byte, err error) error {
	if errors.Is(err, context.Canceled) {
		return failureWithCause(FailureCanceled, "configuration loading was canceled", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return failureWithCause(FailureCanceled, "configuration loading timed out", err)
	}
	var domain *Error
	if errors.As(err, &domain) {
		return err
	}
	if sourceSchemeRE.MatchString(strings.TrimSpace(string(raw))) {
		return failureWithCause(FailureInvalidArgument, "configuration URL could not be fetched", err)
	}
	return failureWithCause(FailureInvalidArgument, "configuration could not be loaded", err)
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
	if m.sourceStore != nil {
		if loaded.Kind == ConfigSourceURL {
			if err := m.sourceStore.Save(ctx, []byte(loaded.SourceURL)); err != nil {
				return ConfigureResult{}, failureWithCause(FailurePlatform, "accepted configuration URL could not be saved", err)
			}
		} else if err := m.sourceStore.Clear(ctx); err != nil {
			return ConfigureResult{}, failureWithCause(FailurePlatform, "saved configuration URL could not be cleared", err)
		}
	}
	s.profiles, s.digest, s.sourceKind, s.sourceURL, s.configured = parsed.profiles, parsed.digest, loaded.Kind, loaded.SourceURL, true
	s.sourceError = ""
	s.active, s.lastFailure, s.lastFailureMessage, s.state, s.cleanupDone, s.cleanupFailed = nil, "", "", StateConfigured, true, false
	s.recovering, s.recoveryOriginGeneration, s.recoveryCount = false, 0, 0
	m.appendLocked(s)
	result := ConfigureResult{Digest: s.digest, Sequence: s.sequence, Profiles: summaries(s.profiles), SourceKind: s.sourceKind}
	return result, nil
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
	// A GUI submits its changed source with Start. Configuration is accepted
	// before tunnel startup, so it remains available if this attempt fails.
	// Clear Source before storing the target for health recovery.
	source := target.Source
	target.Source = nil
	if source != nil {
		if target.Mode != AutoSelect {
			return StartResult{}, failure(FailureInvalidArgument, "a source can only be started with AUTO_SELECT")
		}
		configured, err := m.Configure(requestCtx, sessionID, expectedSequence, source)
		if err != nil {
			return StartResult{}, err
		}
		expectedSequence = configured.Sequence
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
	// Accepted work outlives the request and is canceled by Stop or recovery.
	ctx, cancel := context.WithCancel(context.WithoutCancel(requestCtx))
	s.cancel, s.ledger, s.cleanupDone, s.cleanupFailed, s.active, s.lastFailure, s.lastFailureMessage = cancel, &ledger{}, false, false, nil, "", ""
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
		// The attempt worker owns acquisition and cleanup. A late native Start
		// keeps this generation STOPPING until that worker can release its lease.
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
func (m *Manager) reportHealthFailure(s *session, generation uint64, cause error) {
	if cause == nil {
		cause = errors.New("connected health check failed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation == 0 || generation != s.generation || s.state != StateConnected {
		return
	}
	m.prepareHealthRecoveryLocked(s, generation, cause)
	s.state = StateStopping
	m.appendLocked(s)
	if s.cancel != nil {
		s.cancel()
	}
}

func (m *Manager) prepareHealthRecoveryLocked(s *session, generation uint64, cause error) {
	if s.activeTarget.Mode != AutoSelect {
		s.failureAfterCleanup = FailureRuntime
		s.failureMessageAfterCleanup = fmt.Sprintf("connected health check failed: %v", cause)
		s.recovering = false
		s.recoveryOriginGeneration = 0
		return
	}
	healthFailure := fmt.Sprintf("connected health check failed: %v", cause)
	if s.recovering && s.lastFailureMessage != "" {
		healthFailure = s.lastFailureMessage + "; " + healthFailure
	}
	s.lastFailure = FailureRuntime
	s.lastFailureMessage = healthFailure
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
	s.failureMessageAfterCleanup = fmt.Sprintf("%s: %s", autoRecoveryMessage, healthFailure)
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

type profileSelection struct {
	profile       RuntimeProfile
	platformLease PlatformLease
	runtimeLease  RuntimeLease
}

// runStart is one lifecycle transaction whose branches all preserve generation
// fencing and late-lease cleanup; keeping those checks together is deliberate.
//
//nolint:gocyclo,nestif // Refactoring the transaction would risk cleanup races.
func (m *Manager) runStart(ctx context.Context, s *session, generation uint64, target StartTarget) {
	defer func() {
		s.mu.Lock()
		stopping := s.generation == generation && s.state == StateStopping
		s.mu.Unlock()
		if stopping {
			m.finish(s, generation, failure(FailureCanceled, "attempt stopped"))
		}
	}()
	if target.Mode == ProfileIndex {
		s.mu.Lock()
		profile := s.profiles[target.Index]
		s.mu.Unlock()
		if !m.advance(s, generation, StatePreparing, &profile.Summary) {
			return
		}
	}
	selection, err := m.selectProfile(ctx, s, generation, target)
	if err != nil {
		m.finish(s, generation, err)
		return
	}
	profile := selection.profile
	if selection.platformLease == nil || selection.runtimeLease == nil {
		if cleanupErr := releaseProfileSelection(selection); cleanupErr != nil {
			m.finish(s, generation, wrapFailure(FailureCleanup, cleanupErr))
		} else {
			m.finish(s, generation, failure(FailureRuntime, "profile selection returned incomplete leases"))
		}
		return
	}
	s.mu.Lock()
	if s.generation != generation || s.ledger == nil {
		s.mu.Unlock()
		if cleanupErr := releaseProfileSelection(selection); cleanupErr != nil {
			m.finish(s, generation, wrapFailure(FailureCleanup, cleanupErr))
		}
		return
	}
	s.ledger.push(func(c context.Context) error { return selection.platformLease.Release(c) })
	s.ledger.push(func(c context.Context) error { return selection.runtimeLease.Stop(c) })
	s.mu.Unlock()
	if target.Mode == AutoSelect && !m.advance(s, generation, StatePreparing, &profile.Summary) {
		return
	}
	if !m.advance(s, generation, StateConnected, &profile.Summary) {
		return
	}
	m.waitForAttemptEnd(ctx, s, generation, selection.runtimeLease)
}

// waitForAttemptEnd keeps cleanup with the worker that acquired the lease.
func (m *Manager) waitForAttemptEnd(ctx context.Context, s *session, generation uint64, lease RuntimeLease) {
	var failures <-chan error
	if monitored, ok := lease.(HealthMonitoringLease); ok {
		failures = monitored.HealthFailures()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case cause, ok := <-failures:
			if !ok {
				failures = nil
				continue
			}
			if cause != nil {
				m.reportHealthFailure(s, generation, cause)
				return
			}
		}
	}
}

func (m *Manager) selectProfile(ctx context.Context, s *session, generation uint64, target StartTarget) (profileSelection, error) {
	s.mu.Lock()
	profiles := append([]RuntimeProfile(nil), s.profiles...)
	s.mu.Unlock()
	candidates := profiles
	if target.Mode == ProfileIndex {
		if target.Index < 0 || target.Index >= len(profiles) {
			return profileSelection{}, failure(FailureInvalidArgument, "profile index is out of range")
		}
		candidates = profiles[target.Index : target.Index+1]
	}
	var candidateErrors []error
	for _, profile := range candidates {
		selection, retry, candidateErr := m.selectProfileCandidate(ctx, s, generation, target, profile, candidateErrors)
		if retry {
			candidateErrors = append(candidateErrors, candidateErr)
			continue
		}
		return selection, candidateErr
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return profileSelection{}, failureWithCause(FailureCanceled, "start was canceled", errors.Join(ctxErr, errors.Join(candidateErrors...)))
	}
	return profileSelection{}, failureWithCause(FailureProbe, "no configured profile became ready", errors.Join(candidateErrors...))
}

// selectProfileCandidate owns setup and rollback for one candidate. A retry is
// returned only for an auto-selection runtime failure after full cleanup.
func (m *Manager) selectProfileCandidate(ctx context.Context, s *session, generation uint64, target StartTarget, profile RuntimeProfile, previousErrors []error) (profileSelection, bool, error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return profileSelection{}, false, failureWithCause(FailureCanceled, "start was canceled", errors.Join(ctxErr, errors.Join(previousErrors...)))
	}
	ref := SessionRef{s.id, generation}
	platformLease, prepareErr := m.platform.PrepareTunnel(ctx, ref)
	if prepareErr == nil && platformLease == nil {
		prepareErr = failure(FailurePlatform, "platform returned an empty tunnel lease")
	}
	if prepareErr != nil {
		prepareErr = errors.Join(errors.Join(previousErrors...), prepareErr)
		if platformLease != nil {
			if releaseErr := platformLease.Release(context.Background()); releaseErr != nil {
				return profileSelection{}, false, wrapFailure(FailureCleanup, errors.Join(prepareErr, releaseErr))
			}
		}
		return profileSelection{}, false, wrapFailure(FailurePlatform, prepareErr)
	}
	runtimeLease, startErr := m.runtime.Start(ctx, ref, profile)
	invalidRuntimeLease := startErr == nil && runtimeLease == nil
	if invalidRuntimeLease {
		startErr = failure(FailureRuntime, "runtime returned an empty lease")
	}
	if startErr != nil {
		cleanupErr := releaseProfileSelection(profileSelection{platformLease: platformLease, runtimeLease: runtimeLease})
		if cleanupErr != nil {
			return profileSelection{}, false, wrapFailure(FailureCleanup, errors.Join(errors.Join(previousErrors...), startErr, cleanupErr))
		}
		var cleanupFailure *CleanupFailure
		if errors.As(startErr, &cleanupFailure) || CodeOf(startErr) == FailureCleanup {
			return profileSelection{}, false, wrapFailure(FailureCleanup, errors.Join(errors.Join(previousErrors...), startErr))
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return profileSelection{}, false, failureWithCause(FailureCanceled, "start was canceled", errors.Join(ctxErr, startErr, errors.Join(previousErrors...)))
		}
		if invalidRuntimeLease || target.Mode == ProfileIndex {
			return profileSelection{}, false, wrapFailure(FailureRuntime, errors.Join(errors.Join(previousErrors...), startErr))
		}
		return profileSelection{}, true, fmt.Errorf("profile %d (%s): %w", profile.Summary.Index, profile.Summary.Protocol, startErr)
	}
	return profileSelection{profile: profile, platformLease: platformLease, runtimeLease: runtimeLease}, false, nil
}

func releaseProfileSelection(selection profileSelection) error {
	var err error
	if selection.runtimeLease != nil {
		err = errors.Join(err, selection.runtimeLease.Stop(context.Background()))
	}
	if selection.platformLease != nil {
		err = errors.Join(err, selection.platformLease.Release(context.Background()))
	}
	return err
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
		s.lastFailure, s.lastFailureMessage = "", ""
		s.recovering = false
		s.recoveryOriginGeneration = 0
	}
	m.appendLocked(s)
	s.mu.Unlock()
	return true
}

func (m *Manager) finish(s *session, generation uint64, cause error) {
	m.finishWithPolicy(s, generation, cause)
}

// finishWithPolicy runs only in the attempt worker, after its current native
// operation returns. It drains the generation ledger before another attempt.
func (m *Manager) finishWithPolicy(s *session, generation uint64, cause error) {
	s.mu.Lock()
	if s.generation != generation || (s.state != StateProbing && s.state != StatePreparing && s.state != StateConnected && s.state != StateStopping) {
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
	// Stop may arrive while the ledger is releasing outside the lock.
	wasStopping = wasStopping || s.state == StateStopping
	s.cleanupDone, s.cleanupFailed, s.cancel = true, cleanupErr != nil, nil
	if cleanupErr != nil || CodeOf(cause) == FailureCleanup {
		s.cleanupFailed = true
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
		s.state, s.active = StateIdle, nil
		m.appendLocked(s)
		if restart {
			go m.startFailover(s, generation)
		} else {
			s.recovering, s.recoveryOriginGeneration = false, 0
		}
		return
	}
	message := errorMessage(cause)
	if s.recovering && s.lastFailureMessage != "" {
		message = s.lastFailureMessage + "; automatic recovery failed: " + message
	}
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
	s.cancel, s.ledger, s.cleanupDone, s.cleanupFailed, s.active = cancel, &ledger{}, false, false, nil
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
}

func (m *Manager) get(id string) (*session, error) {
	s := m.session
	if s == nil || s.id == "" {
		return nil, failureWithCause(FailureInternal, "could not allocate a session ID", m.initErr)
	}
	if id != "" && id != s.id {
		return nil, failure(FailureNotFound, "session owner has restarted")
	}
	return s, nil
}
func snapshotLocked(s *session) SnapshotResult {
	return SnapshotResult{
		SessionID: s.id, Sequence: s.sequence, Generation: s.generation, State: s.state,
		Configured: s.configured, Digest: s.digest, SourceKind: s.sourceKind, SourceURL: s.sourceURL, SourceError: s.sourceError,
		Profiles:      summaries(s.profiles),
		ActiveProfile: cloneSummaryPtr(s.active), LastFailure: s.lastFailure,
		LastFailureMessage: s.lastFailureMessage, CleanupComplete: s.cleanupDone,
		Recovering: s.recovering, PrimaryAction: primaryActionLocked(s),
	}
}

func primaryActionLocked(s *session) string {
	if s.state == StateProbing || s.state == StatePreparing || s.state == StateConnected || s.recovering {
		return "STOP"
	}
	if s.state != StateStopping && s.cleanupDone && !s.cleanupFailed {
		return "START"
	}
	return "NONE"
}
func summaries(in []RuntimeProfile) []ProfileSummary {
	out := make([]ProfileSummary, len(in))
	for i := range in {
		out[i] = in[i].Summary
	}
	return out
}
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
	if code == FailureCleanup {
		return failureWithCause(FailureCleanup, "cleanup failed", err)
	}
	var cleanup *CleanupFailure
	if errors.As(err, &cleanup) {
		return failureWithCause(FailureCleanup, "cleanup failed", err)
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

func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate session ID: %w", err)
	}
	return hex.EncodeToString(b), nil
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
