package sessionapi

import (
	"context"
	"errors"
	"time"

	"core/log"
)

const (
	// The network is often not up yet when the service starts at boot, so a
	// restore that cannot reach its source is retried for a short window.
	restoreRetryInterval = 10 * time.Second
	restoreRetryWindow   = 2 * time.Minute

	// SubscriptionUnreachableMessage is shown in the snapshot when the startup
	// restore ran out of retries because the subscription server did not answer.
	SubscriptionUnreachableMessage = "Could not reach the subscription server"
)

// errSourceUnreachable marks a failed source request (no route, DNS failure,
// timeout). Any other loading failure, such as an oversized response, is final.
var errSourceUnreachable = errors.New("configuration source is unreachable")

// restoreClock is replaced by tests so the window runs without real waiting.
type restoreClock struct {
	now   func() time.Time
	sleep func(context.Context, time.Duration) error
}

var realRestoreClock = restoreClock{now: time.Now, sleep: func(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}}

// PrepareRestoreAtServiceStart fixes the user-intent epoch now, before the
// control channel accepts commands, and returns the restore to run afterwards.
// A Configure, Start or Stop accepted in between supersedes the restore.
//
// The restore retries only while the saved source cannot be reached, every
// restoreRetryInterval for restoreRetryWindow. It stops at once on success, on
// any other failure, and when the user acts. A failed connection attempt is not
// retried here; the session's ordinary failure handling owns it. When the
// window runs out, or a final failure other than "nothing saved" occurs, the
// error becomes the idle session's last failure, shown in the snapshot until
// the next Start.
func (m *Manager) PrepareRestoreAtServiceStart() func(context.Context) error {
	s := m.session
	s.mu.Lock()
	epoch := s.restoreEpoch
	s.mu.Unlock()
	return func(ctx context.Context) error {
		_, err := m.restoreWithRetries(ctx, epoch, realRestoreClock)
		return err
	}
}

func (m *Manager) restoreWithRetries(ctx context.Context, epoch uint64, clock restoreClock) (StartResult, error) {
	deadline := clock.now().Add(restoreRetryWindow)
	for attempt := 1; ; attempt++ {
		result, phase, err := m.restoreAttempt(ctx, true, &epoch)
		if err == nil {
			return result, nil
		}
		retry := phase == restorePhaseLoad && errors.Is(err, errSourceUnreachable) && ctx.Err() == nil
		if !retry || clock.now().Add(restoreRetryInterval).After(deadline) {
			log.Error("sessionapi", "automatic restore failed", map[string]any{"attempts": attempt, "failure_code": string(CodeOf(err))})
			m.reportStartupRestoreFailure(ctx, epoch, err)
			return result, err
		}
		if sleepErr := clock.sleep(ctx, restoreRetryInterval); sleepErr != nil {
			return result, errors.Join(err, sleepErr)
		}
	}
}

// reportStartupRestoreFailure shows a failed restore in the idle session's
// last failure, the field the snapshot already reports before the first Start.
// It is skipped when the user acted meanwhile, when a failure is already
// shown, when the restore itself was canceled, and for "nothing saved", which
// is the normal state of a fresh install.
func (m *Manager) reportStartupRestoreFailure(ctx context.Context, epoch uint64, err error) {
	code := CodeOf(err)
	if code == FailureNotConfigured || ctx.Err() != nil {
		return
	}
	text := "automatic restore failed: " + errorMessage(err)
	var domain *Error
	if errors.As(err, &domain) {
		text = "automatic restore failed: " + domain.Message
	}
	if errors.Is(err, errSourceUnreachable) {
		code, text = FailurePlatform, SubscriptionUnreachableMessage
	}
	s := m.session
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.restoreEpoch != epoch || s.closing || s.vpnActive || s.lastFailure != "" || s.pending != nil {
		return
	}
	s.lastFailure, s.lastFailureMessage = code, text
	m.appendLocked(s)
}
