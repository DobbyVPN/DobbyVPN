// Package v2 is the transport-neutral API for a DobbyVPN session.
//
// It deliberately does not bind a protocol implementation.  Desktop gRPC and
// mobile bindings can use the same manager while supplying their own Runtime
// and PlatformAdapter.  In particular, neither an event nor a Snapshot exposes
// a connection URL, credential, or the raw TOML.
package v2

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
)

const (
	APIVersion       = "sessionapi/v2"
	commandConfigure = "configure"
	commandStart     = "start"
	commandStop      = "stop"
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
	StateDestroyed  State = "DESTROYED"
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

// Error is safe for UI/IPC use.  Message must never be populated with a raw
// configuration fragment or a credential.
type Error struct {
	Code    FailureCode
	Message string
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

func failure(code FailureCode, message string) error { return &Error{Code: code, Message: message} }

func CodeOf(err error) FailureCode {
	var target *Error
	if errors.As(err, &target) {
		return target.Code
	}
	return FailureInternal
}

type Capability struct {
	Name    string
	Enabled bool
}

type Capabilities struct {
	Version   string
	Protocols []Protocol
	Features  []Capability
}

// ProfileSummary is safe to return to an untrusted UI.  It intentionally has
// no server/address/config field.
type ProfileSummary struct {
	Index       int
	Protocol    Protocol
	Description string
}

type Warning struct {
	Code    string
	Message string
}

type ConfigureResult struct {
	Digest     string
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

type StartResult struct{ Generation uint64 }

type StopResult struct{ Generation uint64 }

// HealthResult reports the generation which was evaluated. An unhealthy
// AUTO_SELECT generation is released before deterministic failover starts;
// an explicitly selected profile fails without substituting another profile.
type HealthResult struct{ Generation uint64 }

// Event is an append-only, monotonically sequenced transition.  A failure is
// typed so platform code never needs to parse log/error strings.
type Event struct {
	SessionID  string
	Generation uint64
	Sequence   uint64
	State      State
	Profile    *ProfileSummary
	Failure    FailureCode
	Warning    *Warning
}

type SnapshotResult struct {
	SessionID       string
	Generation      uint64
	State           State
	Configured      bool
	ActiveProfile   *ProfileSummary
	LastFailure     FailureCode
	CleanupComplete bool
}

type ObserveResult struct {
	Events       []Event
	NextSequence uint64
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
	// ExcludeCIDRs and PreflightHosts are interpreted once by Go and remain
	// private to the runtime. Platform shells must not parse routing or DNS
	// inputs from the original configuration.
	ExcludeCIDRs   []string
	PreflightHosts []string
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
// The manager deliberately treats this as optional so existing Runtime
// implementations and the public ReportHealth compatibility path keep working.
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
	PublishState(context.Context, Event) error
}

type PlatformLease interface{ Release(context.Context) error }

type ManagerOptions struct {
	Runtime  Runtime
	Platform PlatformAdapter
	Audit    AuditSink
	Loader   ConfigLoader
}

type Manager struct {
	mu       sync.RWMutex
	runtime  Runtime
	platform PlatformAdapter
	loader   ConfigLoader
	audit    *auditRecorder
	sessions map[string]*session
	newID    func() string
	activeID string
}

type commandRecord struct {
	op     string
	config ConfigureResult
	start  StartResult
	stop   StopResult
	err    error
}

type session struct {
	mu sync.Mutex

	id         string
	state      State
	generation uint64
	configured bool
	digest     string
	profiles   []RuntimeProfile
	warnings   []Warning

	active              *ProfileSummary
	lastFailure         FailureCode
	cleanupDone         bool
	cleanupFailed       bool
	cancel              context.CancelFunc
	ledger              *ledger
	workerDone          chan struct{}
	activeTarget        StartTarget
	restartAfterCleanup bool
	failureAfterCleanup FailureCode
	commands            map[string]commandRecord
	events              []Event
	sequence            uint64
	watchers            map[chan Event]struct{}
	done                chan struct{}
	destroyed           bool
	auditState          State
	publisher           *eventPublisher
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
	return &Manager{
		runtime: r, platform: p, loader: loader, audit: newAuditRecorder(options.Audit),
		sessions: make(map[string]*session), newID: randomID,
	}
}

func (m *Manager) GetCapabilities(context.Context) Capabilities {
	operation := m.audit.begin("get_capabilities")
	defer operation.end(nil)
	return Capabilities{
		Version:   APIVersion,
		Protocols: []Protocol{ProtocolOutline, ProtocolXray, ProtocolTrustTunnel},
		Features:  []Capability{{Name: "ordered_events", Enabled: true}, {Name: "idempotent_commands", Enabled: true}},
	}
}

func (m *Manager) CreateSession(context.Context) (id string, err error) {
	operation := m.audit.begin("create_session")
	defer func() { operation.end(err) }()
	id = m.newID()
	if id == "" {
		return "", failure(FailureInternal, "could not allocate a session ID")
	}
	s := &session{
		id: id, state: StateIdle, auditState: StateIdle, cleanupDone: true,
		commands: make(map[string]commandRecord), watchers: make(map[chan Event]struct{}), done: make(chan struct{}), publisher: newEventPublisher(),
	}
	m.mu.Lock()
	if m.activeID != "" {
		m.mu.Unlock()
		return "", failure(FailureConflict, "another session already owns the active tunnel")
	}
	m.sessions[id] = s
	m.mu.Unlock()
	go m.publishEvents(s.publisher) // #nosec G118 -- the publisher intentionally owns the session lifetime, not one request.
	m.audit.snapshot(snapshotLocked(s), "create_session")
	return id, nil
}

// Subscribe provides a push event stream for platform bindings. The returned
// channel is preloaded with retained events after afterSequence and then
// receives every later transition. Close must be called by the subscriber.
func (m *Manager) Subscribe(ctx context.Context, sessionID string, afterSequence uint64) (events <-chan Event, closeSubscription func(), err error) {
	s, err := m.get(sessionID)
	if err != nil {
		return nil, func() {}, err
	}
	ch := make(chan Event, 64)
	s.mu.Lock()
	if s.destroyed {
		s.mu.Unlock()
		return nil, func() {}, failure(FailureNotFound, "session has been destroyed")
	}
	for _, event := range s.events {
		if event.Sequence > afterSequence {
			ch <- cloneEvent(event)
		}
	}
	s.watchers[ch] = struct{}{}
	s.mu.Unlock()
	var once sync.Once
	closeSubscription = func() {
		once.Do(func() {
			s.mu.Lock()
			if _, ok := s.watchers[ch]; ok {
				delete(s.watchers, ch)
				close(ch)
			}
			s.mu.Unlock()
		})
	}
	go func() {
		select {
		case <-ctx.Done():
			closeSubscription()
		case <-s.done:
			closeSubscription()
		}
	}()
	return ch, closeSubscription, nil
}

// RecoverActiveSession returns the one process-owned session which is still
// connected or cleaning up. A caller may use it after a UI/process restart to
// reattach instead of creating a second lifecycle owner. The manager never
// invents a new session here: absence is an explicit NOT_FOUND result.
func (m *Manager) RecoverActiveSession(context.Context) (string, error) {
	m.mu.RLock()
	activeID := m.activeID
	active := m.sessions[activeID]
	m.mu.RUnlock()
	if active != nil {
		active.mu.Lock()
		valid := !active.destroyed && (active.state == StateProbing || active.state == StatePreparing || active.state == StateConnected || active.state == StateStopping || !active.cleanupDone)
		active.mu.Unlock()
		if valid {
			return activeID, nil
		}
	}
	return "", failure(FailureNotFound, "no active session is available for recovery")
}

func (m *Manager) Configure(ctx context.Context, sessionID, commandID string, rawConfig []byte) (result ConfigureResult, err error) {
	operation := m.audit.begin("configure")
	defer func() { operation.end(err) }()
	s, err := m.get(sessionID)
	if err != nil {
		return ConfigureResult{}, err
	}
	if commandID == "" {
		return ConfigureResult{}, failure(FailureInvalidArgument, "command ID is required")
	}
	loaded, loadErr := m.loader.Load(ctx, rawConfig)
	var parsed parsedConfig
	if loadErr == nil {
		parsed, loadErr = parseConfig(loaded.Raw)
	}

	s.mu.Lock()
	if s.destroyed {
		s.mu.Unlock()
		return ConfigureResult{}, failure(FailureNotFound, "session has been destroyed")
	}
	if record, ok := s.commands[commandID]; ok {
		if record.op != commandConfigure {
			s.mu.Unlock()
			return ConfigureResult{}, failure(FailureConflict, "command ID was used by another operation")
		}
		cachedResult, saved := cloneConfigure(record.config), record.err
		s.mu.Unlock()
		return cachedResult, saved
	}
	if s.state == StateProbing || s.state == StatePreparing || s.state == StateConnected || s.state == StateStopping || !s.cleanupDone || s.cleanupFailed {
		err := failure(FailureConflict, "cannot configure until the previous generation cleaned up successfully")
		s.commands[commandID] = commandRecord{op: commandConfigure, err: err}
		s.mu.Unlock()
		return ConfigureResult{}, err
	}
	if loadErr != nil {
		s.commands[commandID] = commandRecord{op: commandConfigure, err: loadErr}
		s.lastFailure = CodeOf(loadErr)
		s.state = StateFailed
		m.appendLocked(s, Event{State: StateFailed, Failure: CodeOf(loadErr)})
		s.mu.Unlock()
		return ConfigureResult{}, loadErr
	}
	s.profiles, s.digest, s.warnings, s.configured = parsed.profiles, parsed.digest, parsed.warnings, true
	s.active, s.lastFailure, s.state, s.cleanupDone, s.cleanupFailed = nil, "", StateConfigured, true, false
	result = ConfigureResult{Digest: s.digest, Profiles: summaries(s.profiles), Warnings: cloneWarnings(s.warnings), SourceKind: loaded.Kind}
	s.commands[commandID] = commandRecord{op: commandConfigure, config: result}
	m.appendLocked(s, Event{State: StateConfigured})
	for i := range s.warnings {
		w := s.warnings[i]
		m.appendLocked(s, Event{State: StateConfigured, Warning: &w})
	}
	s.mu.Unlock()
	return cloneConfigure(result), nil
}

func (m *Manager) Start(requestCtx context.Context, sessionID, commandID string, target StartTarget) (result StartResult, err error) {
	operation := m.audit.begin("start")
	defer func() { operation.end(err) }()
	s, err := m.get(sessionID)
	if err != nil {
		return StartResult{}, err
	}
	if commandID == "" {
		return StartResult{}, failure(FailureInvalidArgument, "command ID is required")
	}
	s.mu.Lock()
	if s.destroyed {
		s.mu.Unlock()
		return StartResult{}, failure(FailureNotFound, "session has been destroyed")
	}
	if record, ok := s.commands[commandID]; ok {
		if record.op != commandStart {
			s.mu.Unlock()
			return StartResult{}, failure(FailureConflict, "command ID was used by another operation")
		}
		cachedResult, saved := record.start, record.err
		s.mu.Unlock()
		return cachedResult, saved
	}
	if !s.configured {
		err := failure(FailureNotConfigured, "configure a session before starting it")
		s.commands[commandID] = commandRecord{op: commandStart, err: err}
		s.mu.Unlock()
		return StartResult{}, err
	}
	if s.state == StateProbing || s.state == StatePreparing || s.state == StateConnected || s.state == StateStopping || !s.cleanupDone || s.cleanupFailed {
		err := failure(FailureConflict, "previous generation has not completed cleanup")
		s.commands[commandID] = commandRecord{op: commandStart, err: err}
		s.mu.Unlock()
		return StartResult{}, err
	}
	if target.Mode != AutoSelect && target.Mode != ProfileIndex {
		err := failure(FailureInvalidArgument, "start mode must be AUTO_SELECT or PROFILE_INDEX")
		s.commands[commandID] = commandRecord{op: commandStart, err: err}
		s.mu.Unlock()
		return StartResult{}, err
	}
	if target.Mode == ProfileIndex && (target.Index < 0 || target.Index >= len(s.profiles)) {
		err := failure(FailureInvalidArgument, "profile index is out of range")
		s.commands[commandID] = commandRecord{op: commandStart, err: err}
		s.mu.Unlock()
		return StartResult{}, err
	}
	m.mu.Lock()
	if m.activeID != "" && m.activeID != s.id {
		err := failure(FailureConflict, "another session already owns the active tunnel")
		s.commands[commandID] = commandRecord{op: commandStart, err: err}
		m.mu.Unlock()
		s.mu.Unlock()
		return StartResult{}, err
	}
	m.activeID = s.id
	m.mu.Unlock()

	s.generation++
	generation := s.generation
	ctx, cancel := context.WithCancel(context.WithoutCancel(requestCtx))
	s.cancel, s.ledger, s.workerDone, s.cleanupDone, s.cleanupFailed, s.active, s.lastFailure = cancel, &ledger{}, make(chan struct{}), false, false, nil, ""
	s.activeTarget, s.restartAfterCleanup, s.failureAfterCleanup = target, false, ""
	s.state = StateProbing
	result = StartResult{Generation: generation}
	s.commands[commandID] = commandRecord{op: commandStart, start: result}
	m.appendLocked(s, Event{Generation: generation, State: StateProbing})
	s.mu.Unlock()
	go m.runStart(ctx, s, generation, target) // #nosec G118 -- generation work intentionally outlives the initiating request and owns its cancel function.
	return result, nil
}

func (m *Manager) Stop(_ context.Context, sessionID, commandID string, generation uint64) (result StopResult, err error) {
	operation := m.audit.begin("stop")
	defer func() { operation.end(err) }()
	s, err := m.get(sessionID)
	if err != nil {
		return StopResult{}, err
	}
	if commandID == "" {
		return StopResult{}, failure(FailureInvalidArgument, "command ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.destroyed {
		return StopResult{}, failure(FailureNotFound, "session has been destroyed")
	}
	if record, ok := s.commands[commandID]; ok {
		if record.op != commandStop {
			return StopResult{}, failure(FailureConflict, "command ID was used by another operation")
		}
		return record.stop, record.err
	}
	if generation != s.generation || generation == 0 {
		err := failure(FailureStaleGeneration, "generation is not active for this session")
		s.commands[commandID] = commandRecord{op: commandStop, err: err}
		return StopResult{}, err
	}
	if s.state == StateIdle || s.state == StateConfigured || s.state == StateFailed {
		// A runtime-owned health failure can finish cleanup before the mobile
		// caller gets to issue its ordinary stop request. Once this exact
		// generation is fully cleaned, stop is an idempotent acknowledgement;
		// it must not turn a clean terminal state into a misleading stale-stop
		// failure. Cleanup failures remain errors and still block restart.
		if s.generation == generation && s.cleanupDone && !s.cleanupFailed && s.state != StateConfigured {
			result = StopResult{Generation: generation}
			s.commands[commandID] = commandRecord{op: commandStop, stop: result}
			return result, nil
		}
		err := failure(FailureStaleGeneration, "generation is no longer active")
		s.commands[commandID] = commandRecord{op: commandStop, err: err}
		return StopResult{}, err
	}
	result = StopResult{Generation: generation}
	s.commands[commandID] = commandRecord{op: commandStop, stop: result}
	if s.state != StateStopping {
		s.state = StateStopping
		m.appendLocked(s, Event{Generation: generation, State: StateStopping})
		if s.cancel != nil {
			s.cancel()
		}
		// The start worker owns every acquisition until it exits.  A runtime that
		// ignores cancellation must keep this generation STOPPING, rather than
		// allowing a new TUN/runtime to overlap it.
		done := s.workerDone
		go func() { <-done; m.finishAfterStop(s, generation, nil, failure(FailureCanceled, "stop requested")) }()
	}
	return result, nil
}

// ProtectSocket is the session API path for platform socket protection.
// Runtimes normally receive this as a closure from their binding before each
// non-loopback protocol dial. A protection failure aborts that dial.
func (m *Manager) ProtectSocket(ctx context.Context, ref SessionRef, fd int, loopback bool) (err error) {
	operation := m.audit.begin("protect_socket")
	defer func() { operation.end(err) }()
	if fd < 0 {
		return failure(FailureInvalidArgument, "socket descriptor must be non-negative")
	}
	s, err := m.get(ref.SessionID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	valid := !s.destroyed && s.generation == ref.Generation && (s.state == StateProbing || s.state == StatePreparing || s.state == StateConnected)
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

// ReportHealth accepts a generation-correlated health result. An unhealthy
// connected generation is fully cleaned up before AUTO_SELECT failover. A
// PROFILE_INDEX generation fails in place so its requested profile identity
// cannot silently change beneath a caller.
func (m *Manager) ReportHealth(_ context.Context, sessionID string, generation uint64, healthy bool) (result HealthResult, err error) {
	operation := m.audit.begin("report_health")
	defer func() { operation.end(err) }()
	s, err := m.get(sessionID)
	if err != nil {
		return HealthResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.destroyed {
		return HealthResult{}, failure(FailureNotFound, "session has been destroyed")
	}
	if generation == 0 || generation != s.generation || s.state != StateConnected {
		return HealthResult{}, failure(FailureStaleGeneration, "health result is not for the connected generation")
	}
	result = HealthResult{Generation: generation}
	if healthy {
		m.appendLocked(s, Event{Generation: generation, State: StateConnected, Profile: cloneSummaryPtr(s.active)})
		return result, nil
	}
	m.appendLocked(s, Event{Generation: generation, State: StateConnected, Profile: cloneSummaryPtr(s.active), Failure: FailureRuntime})
	if s.activeTarget.Mode == AutoSelect {
		s.restartAfterCleanup = true
	} else {
		s.failureAfterCleanup = FailureRuntime
	}
	s.state = StateStopping
	m.appendLocked(s, Event{Generation: generation, State: StateStopping})
	if s.cancel != nil {
		s.cancel()
	}
	done := s.workerDone
	go func() {
		<-done
		m.finishAfterStop(s, generation, nil, failure(FailureCanceled, "health check requested failover"))
	}()
	return result, nil
}

func (m *Manager) Snapshot(_ context.Context, sessionID string) (result SnapshotResult, err error) {
	operation := m.audit.begin("snapshot")
	defer func() { operation.end(err) }()
	s, err := m.get(sessionID)
	if err != nil {
		return SnapshotResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.destroyed {
		return SnapshotResult{}, failure(FailureNotFound, "session has been destroyed")
	}
	result = snapshotLocked(s)
	m.audit.snapshot(result, "snapshot")
	return result, nil
}

func (m *Manager) Observe(_ context.Context, sessionID string, afterSequence uint64) (result ObserveResult, err error) {
	operation := m.audit.begin("observe")
	defer func() { operation.end(err) }()
	s, err := m.get(sessionID)
	if err != nil {
		return ObserveResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.destroyed {
		return ObserveResult{}, failure(FailureNotFound, "session has been destroyed")
	}
	i := sort.Search(len(s.events), func(i int) bool { return s.events[i].Sequence > afterSequence })
	events := make([]Event, len(s.events)-i)
	copy(events, s.events[i:])
	return ObserveResult{Events: events, NextSequence: s.sequence}, nil
}

// DestroySession is only valid after cleanup.  Keeping an active session
// addressable prevents an old callback from being mistaken for a new session.
func (m *Manager) DestroySession(_ context.Context, sessionID string) (err error) {
	operation := m.audit.begin("destroy_session")
	defer func() {
		operation.endAndFlush(err, defaultAuditFlushTimeout)
	}()
	s, err := m.get(sessionID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if !s.cleanupDone || s.cleanupFailed || s.state == StateProbing || s.state == StatePreparing || s.state == StateConnected || s.state == StateStopping {
		s.mu.Unlock()
		return failure(FailureConflict, "successful cleanup is required before destroying a session")
	}
	s.destroyed, s.state = true, StateDestroyed
	m.appendLocked(s, Event{Generation: s.generation, State: StateDestroyed})
	close(s.done)
	for ch := range s.watchers {
		delete(s.watchers, ch)
		close(ch)
	}
	s.publisher.close()
	s.mu.Unlock()
	m.mu.Lock()
	delete(m.sessions, sessionID)
	m.mu.Unlock()
	return nil
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
			if s.generation == generation && !s.destroyed && s.ledger != nil && (s.state == StatePreparing || s.state == StateStopping) {
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
	if s.generation == generation && s.state == StateStopping && !s.destroyed && s.ledger != nil {
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
			if s.generation == generation && !s.destroyed && s.ledger != nil && (s.state == StatePreparing || s.state == StateStopping) {
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
	if s.generation == generation && s.state == StateStopping && !s.destroyed && s.ledger != nil {
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
		go m.watchRuntimeHealth(ctx, s, generation, monitored)
	}
}

// watchRuntimeHealth only receives a notification after the runtime's local
// threshold is reached. ReportHealth retains its public behavior and provides
// the generation/state fence against a late result from a stopped or destroyed
// lease.
func (m *Manager) watchRuntimeHealth(ctx context.Context, s *session, generation uint64, lease HealthMonitoringLease) {
	failures := lease.HealthFailures()
	if failures == nil {
		return
	}
	for range failures {
		_, _ = m.ReportHealth(ctx, s.id, generation, false)
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
			m.appendProbeEvent(s, generation, profile, FailurePlatform)
			return RuntimeProfile{}, wrapFailure(FailurePlatform, prepareErr)
		}
		result, probeErr := m.runtime.Probe(ctx, ref, profile)
		releaseErr := platformLease.Release(context.Background())
		if releaseErr != nil {
			m.appendProbeEvent(s, generation, profile, FailurePlatform)
			return RuntimeProfile{}, wrapFailure(FailurePlatform, releaseErr)
		}
		err := probeErr
		m.appendProbeEvent(s, generation, profile, func() FailureCode {
			if err != nil {
				return FailureProbe
			}
			return ""
		}())
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

func (m *Manager) appendProbeEvent(s *session, generation uint64, profile RuntimeProfile, failureCode FailureCode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation != generation || s.state != StateProbing || s.destroyed {
		return
	}
	event := Event{Generation: generation, State: StateProbing, Profile: cloneSummaryPtr(&profile.Summary), Failure: failureCode}
	m.appendLocked(s, event)
}

func (m *Manager) advance(s *session, generation uint64, state State, profile *ProfileSummary) bool {
	s.mu.Lock()
	if s.generation != generation || s.state == StateStopping || s.destroyed {
		s.mu.Unlock()
		return false
	}
	s.state, s.active = state, cloneSummaryPtr(profile)
	m.appendLocked(s, Event{Generation: generation, State: state, Profile: cloneSummaryPtr(profile)})
	s.mu.Unlock()
	return true
}

func (m *Manager) finish(s *session, generation uint64, cause error) {
	m.finishWithPolicy(s, generation, nil, cause, false)
}

// finishAfterStop is only called after workerDone closes. It is the sole path
// allowed to drain a STOPPING generation, which prevents a late noncooperative
// Runtime.Start from overlapping a newer generation.
func (m *Manager) finishAfterStop(s *session, generation uint64, profile *ProfileSummary, cause error) {
	m.finishWithPolicy(s, generation, profile, cause, true)
}

func (m *Manager) finishWithPolicy(s *session, generation uint64, profile *ProfileSummary, cause error, allowStopping bool) {
	s.mu.Lock()
	if s.generation != generation || s.destroyed || (s.state != StateProbing && s.state != StatePreparing && s.state != StateConnected && s.state != StateStopping) {
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
	if s.generation != generation || s.destroyed {
		return
	}
	s.cleanupDone, s.cleanupFailed, s.cancel = true, cleanupErr != nil, nil
	if cleanupErr != nil {
		s.restartAfterCleanup, s.failureAfterCleanup = false, ""
		s.state, s.active, s.lastFailure = StateFailed, nil, FailureCleanup
		m.appendLocked(s, Event{Generation: generation, State: StateFailed, Profile: cloneSummaryPtr(profile), Failure: FailureCleanup})
		return
	}
	if wasStopping || cause != nil && CodeOf(cause) == FailureCanceled {
		restart := s.restartAfterCleanup
		terminalFailure := s.failureAfterCleanup
		s.restartAfterCleanup, s.failureAfterCleanup = false, ""
		if terminalFailure != "" {
			s.state, s.active, s.lastFailure = StateFailed, nil, terminalFailure
			m.appendLocked(s, Event{Generation: generation, State: StateFailed, Profile: cloneSummaryPtr(profile), Failure: terminalFailure})
			m.clearActive(s.id)
			return
		}
		s.state, s.active, s.lastFailure = StateIdle, nil, ""
		m.appendLocked(s, Event{Generation: generation, State: StateIdle})
		m.clearActive(s.id)
		if restart {
			go m.startFailover(s)
		}
		return
	}
	s.state, s.active, s.lastFailure = StateFailed, nil, CodeOf(cause)
	m.appendLocked(s, Event{Generation: generation, State: StateFailed, Profile: cloneSummaryPtr(profile), Failure: CodeOf(cause)})
	m.clearActive(s.id)
}

func (m *Manager) clearActive(id string) {
	m.mu.Lock()
	if m.activeID == id {
		m.activeID = ""
	}
	m.mu.Unlock()
}

func (m *Manager) startFailover(s *session) {
	s.mu.Lock()
	if s.destroyed || !s.configured || !s.cleanupDone || s.state != StateIdle {
		s.mu.Unlock()
		return
	}
	// The failed generation has already cleared the manager's active-session
	// pointer as part of cleanup. Reclaim it before launching the replacement
	// generation so status/recovery calls continue to address the same session
	// while AUTO_SELECT failover is probing and reconnecting.
	m.mu.Lock()
	if m.activeID != "" && m.activeID != s.id {
		m.mu.Unlock()
		s.mu.Unlock()
		return
	}
	m.activeID = s.id
	m.mu.Unlock()
	s.generation++
	generation := s.generation
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel, s.ledger, s.workerDone, s.cleanupDone, s.cleanupFailed, s.active, s.lastFailure = cancel, &ledger{}, make(chan struct{}), false, false, nil, ""
	s.activeTarget, s.restartAfterCleanup, s.failureAfterCleanup = StartTarget{Mode: AutoSelect}, false, ""
	s.state = StateProbing
	m.appendLocked(s, Event{Generation: generation, State: StateProbing})
	s.mu.Unlock()
	go m.runStart(ctx, s, generation, StartTarget{Mode: AutoSelect})
}

func (m *Manager) appendLocked(s *session, e Event) {
	s.sequence++
	e.SessionID, e.Sequence = s.id, s.sequence
	s.events = append(s.events, e)

	eventName := AuditEventStatusSnapshot
	if s.auditState != e.State {
		eventName = AuditEventStateTransition
	}
	auditEvent := AuditEvent{
		Event: eventName, Generation: e.Generation, Sequence: e.Sequence,
		PreviousState: s.auditState, State: e.State, Configured: s.configured,
		CleanupComplete: s.cleanupDone, Failure: e.Failure,
	}
	if e.Profile != nil {
		auditEvent.HasProfile = true
		auditEvent.ProfileIndex = e.Profile.Index
		auditEvent.Protocol = e.Profile.Protocol
	}
	if e.Warning != nil {
		auditEvent.WarningCode = e.Warning.Code
	}
	s.auditState = e.State
	m.audit.record(auditEvent)

	// Queueing is deliberately non-blocking while the session mutex is held.
	// Platform callbacks are advisory delivery; Observe/Subscribe retain the
	// authoritative ordered ledger for reconnect and gap recovery. A slow
	// callback must never stall lifecycle ownership or cleanup.
	s.publisher.enqueue(e)
	for ch := range s.watchers {
		select {
		case ch <- cloneEvent(e):
		default:
			// A subscriber which cannot keep up must reconnect from the
			// retained event ledger rather than stall the lifecycle mutex.
			delete(s.watchers, ch)
			close(ch)
		}
	}
}

func (m *Manager) publishEvents(publisher *eventPublisher) {
	for {
		event, ok := publisher.next()
		if !ok {
			return
		}
		_ = m.platform.PublishState(context.Background(), event)
	}
}

func (m *Manager) get(id string) (*session, error) {
	m.mu.RLock()
	s := m.sessions[id]
	m.mu.RUnlock()
	if s == nil {
		return nil, failure(FailureNotFound, "session does not exist")
	}
	return s, nil
}
func snapshotLocked(s *session) SnapshotResult {
	return SnapshotResult{SessionID: s.id, Generation: s.generation, State: s.state, Configured: s.configured, ActiveProfile: cloneSummaryPtr(s.active), LastFailure: s.lastFailure, CleanupComplete: s.cleanupDone}
}
func summaries(in []RuntimeProfile) []ProfileSummary {
	out := make([]ProfileSummary, len(in))
	for i := range in {
		out[i] = in[i].Summary
	}
	return out
}
func cloneConfigure(in ConfigureResult) ConfigureResult {
	return ConfigureResult{Digest: in.Digest, Profiles: append([]ProfileSummary(nil), in.Profiles...), Warnings: cloneWarnings(in.Warnings), SourceKind: in.SourceKind}
}
func cloneWarnings(in []Warning) []Warning { return append([]Warning(nil), in...) }
func cloneSummaryPtr(in *ProfileSummary) *ProfileSummary {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneEvent(in Event) Event {
	out := in
	out.Profile = cloneSummaryPtr(in.Profile)
	if in.Warning != nil {
		warning := *in.Warning
		out.Warning = &warning
	}
	return out
}
func wrapFailure(code FailureCode, err error) error {
	if err == nil {
		return nil
	}
	var domain *Error
	if errors.As(err, &domain) {
		return err
	}
	return failure(code, "operation failed")
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
	var first error
	for i := len(l.closers) - 1; i >= 0; i-- {
		if err := l.closers[i](ctx); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// eventPublisher is an ordered, unbounded hand-off between the authoritative
// session ledger and the platform callback. It intentionally never invokes a
// platform implementation while holding the session mutex: a slow callback
// must not block Configure/Start/Stop or prevent cleanup. Closing drains all
// already-enqueued events before the publisher exits, so Destroyed remains the
// final callback in sequence order.
type eventPublisher struct {
	mu     sync.Mutex
	cond   *sync.Cond
	queue  []Event
	closed bool
}

func newEventPublisher() *eventPublisher {
	publisher := &eventPublisher{}
	publisher.cond = sync.NewCond(&publisher.mu)
	return publisher
}

func (p *eventPublisher) enqueue(event Event) {
	p.mu.Lock()
	if !p.closed {
		p.queue = append(p.queue, event)
		p.cond.Signal()
	}
	p.mu.Unlock()
}

func (p *eventPublisher) next() (Event, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for len(p.queue) == 0 && !p.closed {
		p.cond.Wait()
	}
	if len(p.queue) == 0 {
		return Event{}, false
	}
	event := p.queue[0]
	p.queue[0] = Event{}
	p.queue = p.queue[1:]
	return event, true
}

func (p *eventPublisher) close() {
	p.mu.Lock()
	p.closed = true
	p.cond.Broadcast()
	p.mu.Unlock()
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
func (noopPlatform) PublishState(context.Context, Event) error            { return nil }

type noopLease struct{}

func (noopLease) Release(context.Context) error { return nil }
