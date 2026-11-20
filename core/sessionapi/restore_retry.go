package sessionapi

import (
	"context"
	"errors"
	"io"
	"net"
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

// errRestoreLoad marks a restore that failed while loading its saved source,
// before any connection attempt exists. A started connection is owned by the
// session's ordinary failure handling and is never retried here.
var errRestoreLoad = errors.New("saved source could not be loaded")

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
// The restore retries any failure to load the saved source, every
// restoreRetryInterval for restoreRetryWindow. It stops at once on success, on
// a final failure (nothing saved, activation disabled, configuration or saved
// selection unusable), and when the user acts or the restore is canceled. A failed connection attempt is not
// retried here; the session's ordinary failure handling owns it. When the
// window runs out, or a final failure other than "nothing saved" occurs, the
// error becomes the idle session's last failure, shown in the snapshot until
// the next Start.
func (m *Manager) PrepareRestoreAtServiceStart() func(context.Context) error {
	s := m.session
	s.mu.Lock()
	epoch := s.restoreEpoch
	s.serviceRestorePending = true
	s.mu.Unlock()
	return func(ctx context.Context) error {
		defer func() {
			s.mu.Lock()
			s.serviceRestorePending = false
			s.mu.Unlock()
		}()
		_, err := m.restoreWithRetries(ctx, epoch, realRestoreClock)
		return err
	}
}

func (m *Manager) restoreWithRetries(ctx context.Context, epoch uint64, clock restoreClock) (StartResult, error) {
	deadline := clock.now().Add(restoreRetryWindow)
	for attempt := 1; ; attempt++ {
		result, err := m.restoreAttempt(ctx, true, &epoch)
		if err == nil {
			return result, nil
		}
		retry := errors.Is(err, errRestoreLoad) && ctx.Err() == nil && !finalRestoreFailure(err)
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

func finalRestoreFailure(err error) bool {
	code := CodeOf(err)
	return code == FailureNotConfigured || code == FailureMalformedConfig || code == FailureConflict || code == FailureUnsupported
}

// unreachableSource reports a network failure or timeout, including one while
// the response body is read, as opposed to a server that answered.
func unreachableSource(err error) bool {
	var op *net.OpError
	var dns *net.DNSError
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &op) || errors.As(err, &dns)
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
	if unreachableSource(err) {
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

// A restored connection can refresh its inventory in place once connectivity
// returns. Fetch outside the lock and fence the write exactly like user loads.
func (m *Manager) refreshAfterOfflineRestore(ctx context.Context, source string, epoch, generation uint64) {
	loaded, parsed, err := loadConfig(ctx, m.loader, []byte(source))
	s := m.session
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.restoreEpoch != epoch || s.generation != generation || s.stopRequested || s.closing ||
		!s.vpnActive || s.state != StateConnected || s.sourceURL != source || s.offlineAcceptedAt == "" {
		return
	}
	if err == nil && (loaded.SourceURL != source || loaded.Kind != ConfigSourceURL) {
		err = failure(FailureConflict, "recovered configuration source changed")
	}
	if err == nil {
		_, err = m.acceptConfigurationLocked(ctx, s, loaded, parsed, false)
	}
	if err != nil {
		log.Error("sessionapi", "offline inventory refresh failed", map[string]any{logGeneration: generation, "failure_code": string(CodeOf(err))})
	}
}
