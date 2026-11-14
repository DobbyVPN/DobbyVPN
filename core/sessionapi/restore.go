package sessionapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"core/log"
)

// ActivationStore persists Go-owned selection intent for OS-started mobile
// restoration. Native storage never interprets profiles or chooses a protocol.
type ActivationStore interface {
	LoadActivation(context.Context) ([]byte, error)
	SaveActivation(context.Context, []byte) error
}

type savedActivation struct {
	Version int       `json:"version"`
	Enabled bool      `json:"enabled"`
	Mode    StartMode `json:"mode"`
	Index   int       `json:"index"`
	Digest  string    `json:"digest,omitempty"`
	Source  string    `json:"source,omitempty"`
}

func (s FileSourceStore) LoadActivation(ctx context.Context) ([]byte, error) {
	s.Path += ".activation.json"
	return s.Load(ctx)
}
func (s FileSourceStore) SaveActivation(ctx context.Context, raw []byte) error {
	s.Path += ".activation.json"
	return s.Save(ctx, raw)
}

func decodeActivation(raw []byte) (savedActivation, error) {
	var saved savedActivation
	if len(raw) == 0 {
		return saved, failure(FailureNotConfigured, "connect a saved configuration before automatic startup")
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		return saved, failureWithCause(FailureMalformedConfig, "saved activation is invalid", err)
	}
	if !saved.Enabled && (saved.Version == 1 || saved.Version == 2 && saved.Source == "") {
		return saved, nil
	}
	if saved.Version == 1 {
		return saved, failure(FailureConflict, "saved automatic startup has no source identity; connect again")
	}
	if saved.Version != 2 || saved.Source == "" || saved.Digest == "" || saved.Mode != AutoSelect && saved.Mode != ProfileIndex || saved.Mode == ProfileIndex && saved.Index < 0 {
		return saved, failure(FailureMalformedConfig, "saved activation selection is invalid")
	}
	return saved, nil
}

// Called with session.mu held, in the same order as accepted Start/Stop
// commands. Internal health recovery and OS teardown do not change user intent.
func (m *Manager) saveActivationLocked(ctx context.Context, kind ConfigSourceKind, source, digest string, target StartTarget) error {
	store, ok := m.sourceStore.(ActivationStore)
	if !ok {
		return nil
	}
	saved := savedActivation{Version: 2}
	if kind == ConfigSourceURL && source != "" {
		saved = savedActivation{Version: 2, Enabled: true, Mode: target.Mode, Index: target.Index, Digest: digest, Source: source}
	}
	raw, err := json.Marshal(saved)
	if err == nil {
		err = store.SaveActivation(ctx, raw)
	}
	if err != nil {
		return failureWithCause(FailurePlatform, "automatic startup selection could not be saved", err)
	}
	return nil
}
func (m *Manager) stopActivationLocked(ctx context.Context) error {
	store, ok := m.sourceStore.(ActivationStore)
	if !ok {
		return nil
	}
	previous, loadErr := store.LoadActivation(ctx)
	saved := savedActivation{Version: 2}
	if decoded, err := decodeActivation(previous); err == nil && decoded.Version == 2 && decoded.Source != "" {
		saved = decoded
		saved.Enabled = false
	}
	raw, err := json.Marshal(saved)
	if err == nil {
		err = store.SaveActivation(ctx, raw)
	}
	if combined := errors.Join(loadErr, err); combined != nil {
		return failureWithCause(FailurePlatform, "automatic restart could not be disabled", combined)
	}
	return nil
}

// Restore is an OS lifecycle entry point, not a fifth control command. A
// service restart honors the previous explicit Stop; an OS/user/On Demand
// activation can request the last selection regardless of that restart bit.
// Manual selections require the exact inventory, so a changed subscription
// can never silently connect a different profile at the same numeric index.
func (m *Manager) Restore(ctx context.Context, onlyPreviouslyEnabled bool) (StartResult, error) {
	result, _, err := m.restoreAttempt(ctx, onlyPreviouslyEnabled, nil)
	return result, err
}

// restorePhase tells a retrying caller how far one attempt got. Only failures
// before a connection attempt exists (reading the saved activation, loading its
// source) can be transient; a started connection is owned by the session's
// ordinary failure handling.
type restorePhase int

const (
	restorePhaseLoad restorePhase = iota
	restorePhaseConnect
)

// restoreAttempt is one restore. A non-nil fixedEpoch pins the user-intent
// epoch of the first attempt, so a retry never overrides a later user action.
func (m *Manager) restoreAttempt(ctx context.Context, onlyPreviouslyEnabled bool, fixedEpoch *uint64) (StartResult, restorePhase, error) {
	s := m.session
	s.mu.Lock()
	if s.vpnActive && !s.stopRequested && !s.closing {
		result := StartResult{Generation: s.generation, Sequence: s.sequence}
		s.mu.Unlock()
		return result, restorePhaseConnect, nil
	}
	if s.closing || !s.cleanupDone {
		s.mu.Unlock()
		return StartResult{}, restorePhaseConnect, failure(FailureConflict, "previous VPN owner has not completed cleanup")
	}
	store, ok := m.sourceStore.(ActivationStore)
	sessionID, sequence, epoch := s.id, s.sequence, s.restoreEpoch
	if fixedEpoch != nil {
		epoch = *fixedEpoch
	}
	s.mu.Unlock()
	if !ok {
		return StartResult{}, restorePhaseLoad, failure(FailureNotConfigured, "automatic startup storage is unavailable")
	}
	raw, err := store.LoadActivation(ctx)
	if err != nil {
		return StartResult{}, restorePhaseLoad, failureWithCause(FailurePlatform, "saved activation could not be read", err)
	}
	saved, err := decodeActivation(raw)
	if err != nil {
		return StartResult{}, restorePhaseLoad, err
	}
	if !saved.Enabled && (onlyPreviouslyEnabled || saved.Version == 1 || saved.Source == "") {
		return StartResult{}, restorePhaseConnect, nil
	}
	configured, err := m.configure(ctx, sessionID, sequence, []byte(saved.Source), &epoch)
	if err != nil {
		return StartResult{}, restorePhaseLoad, err
	}
	if saved.Mode == ProfileIndex && configured.Digest != saved.Digest {
		return StartResult{}, restorePhaseConnect, failure(FailureConflict, "saved profile inventory changed; select the profile again")
	}
	log.Info("sessionapi", "OS activation requested", map[string]any{"mode": saved.Mode, "profile_index": saved.Index, "inventory_digest": configured.Digest})
	result, err := m.start(ctx, sessionID, configured.Sequence, StartTarget{Mode: saved.Mode, Index: saved.Index, Digest: configured.Digest}, &epoch)
	if err != nil {
		return result, restorePhaseConnect, err
	}
	return result, restorePhaseConnect, waitForRestoredNetwork(ctx, s, result.Generation)
}

func waitForRestoredNetwork(ctx context.Context, s *session, expectedGeneration uint64) error {
	// Report native startup ready when protection exists. Protocol verification
	// continues under the unchanged Go attempt and recovery budgets.
	for {
		s.mu.Lock()
		active, failed, closing, message, changed := s.vpnActive, s.state == StateFailed, s.closing, s.lastFailureMessage, s.changed
		generation := s.generation
		stopped := s.stopRequested || s.cleanupDone
		s.mu.Unlock()
		if active && !closing && !stopped {
			return nil
		}
		if failed || closing || stopped {
			return fmt.Errorf("automatic activation failed: %s", message)
		}
		if generation != expectedGeneration {
			return failure(FailureStaleGeneration, "automatic activation was replaced")
		}
		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), failure(FailureCanceled, "automatic activation canceled"))
		case <-changed:
		}
	}
}
