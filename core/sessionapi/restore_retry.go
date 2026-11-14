package sessionapi

import (
	"context"
	"errors"
	"time"

	"core/log"
)

const (
	// RestoreRetryInterval is the pause between restore attempts after a service
	// start. The network is often not up yet when the service starts at boot.
	RestoreRetryInterval = 10 * time.Second
	// RestoreRetryWindow is how long after the first attempt restoring is retried.
	RestoreRetryWindow = 2 * time.Minute
)

// RestoreRetryPolicy bounds the retry window. Now and Sleep exist so tests
// can drive the window without real waiting; zero values use the real clock.
type RestoreRetryPolicy struct {
	Interval time.Duration
	Window   time.Duration
	Now      func() time.Time
	Sleep    func(context.Context, time.Duration) error
}

// DefaultRestoreRetryPolicy is the policy used by the desktop service.
func DefaultRestoreRetryPolicy() RestoreRetryPolicy {
	return RestoreRetryPolicy{Interval: RestoreRetryInterval, Window: RestoreRetryWindow}
}

func (p RestoreRetryPolicy) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p RestoreRetryPolicy) sleep(ctx context.Context, d time.Duration) error {
	if p.Sleep != nil {
		return p.Sleep(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// RestoreAtServiceStart restores a previously active session after the service
// starts. Failures before a connection attempt exists (the saved source cannot
// be loaded yet) are retried every policy.Interval for policy.Window. It stops
// at once on success, on a failure that cannot be transient (no saved
// activation, a disabled one, a broken saved selection or configuration), and
// when the user starts, stops or configures a session. Once the window is
// exhausted, the last error becomes the idle session's last failure, shown in
// the snapshot until the next Start. A failed connection attempt is not retried
// here; the session's ordinary failure handling owns it.
func (m *Manager) RestoreAtServiceStart(ctx context.Context, policy RestoreRetryPolicy) (StartResult, error) {
	s := m.session
	s.mu.Lock()
	epoch := s.restoreEpoch
	s.mu.Unlock()
	deadline := policy.now().Add(policy.Window)
	for attempt := 1; ; attempt++ {
		result, phase, err := m.restoreAttempt(ctx, true, &epoch)
		if err == nil {
			return result, nil
		}
		if phase != restorePhaseLoad || !transientRestoreFailure(err) {
			m.reportStartupRestoreFailure(epoch, err)
			return result, err
		}
		if policy.now().Add(policy.Interval).After(deadline) {
			log.Error("sessionapi", "automatic restore gave up", map[string]any{"attempts": attempt, "failure_code": string(CodeOf(err))})
			m.reportStartupRestoreFailure(epoch, err)
			return result, err
		}
		log.Info("sessionapi", "automatic restore will be retried", map[string]any{"attempt": attempt, "failure_code": string(CodeOf(err))})
		if sleepErr := policy.sleep(ctx, policy.Interval); sleepErr != nil {
			return result, errors.Join(err, sleepErr)
		}
	}
}

func transientRestoreFailure(err error) bool {
	code := CodeOf(err)
	return code != FailureNotConfigured && code != FailureMalformedConfig && code != FailureConflict &&
		code != FailureCanceled && code != FailureUnsupported
}

// reportStartupRestoreFailure shows a failed restore in the idle session's
// last failure, the field the snapshot already reports before the first Start.
// It is skipped when the user acted meanwhile, when a failure is already
// shown, and for "nothing saved", which is the normal state of a fresh install.
func (m *Manager) reportStartupRestoreFailure(epoch uint64, err error) {
	code := CodeOf(err)
	if code == FailureNotConfigured || code == FailureCanceled {
		return
	}
	message := errorMessage(err)
	var domain *Error
	if errors.As(err, &domain) {
		message = domain.Message
	}
	s := m.session
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.restoreEpoch != epoch || s.closing || s.vpnActive || s.lastFailure != "" || s.pending != nil {
		return
	}
	s.lastFailure, s.lastFailureMessage = code, "automatic restore failed: "+message
	m.appendLocked(s)
}
