package sessionapi

import (
	"time"

	"core/log"
)

// A connection that fails for a network reason leaves the session FAILED with
// the tunnel kept, so no traffic leaks. The session then retries by itself
// with a growing pause, as a user pressing Retry would, until a connection
// succeeds or the user acts.
var autoRetryDelays = []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute}

// autoRetryAfterFunc arms the timers of a new Manager. Test suites whose
// sessions end FAILED without exercising automatic retries replace it, so a
// retry from one test does not run during another.
var autoRetryAfterFunc = func(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop }

// autoRetryProvenHealthy is how long a connection must stay up before the
// pause schedule starts again, so a server that connects and then hangs keeps
// the growing pauses.
const autoRetryProvenHealthy = 30 * time.Second

func autoRetryDelay(attempt int) time.Duration {
	return autoRetryDelays[min(attempt, len(autoRetryDelays)-1)]
}

// autoRetryable reports the failures a later attempt can fix: no profile
// became ready, or the runtime or its connected-health checks failed. These
// are the results of an unreachable server or a resolver that does not
// answer. Every other code needs the user or the system: an unusable
// subscription or selection, missing VPN permission or another platform
// failure, a failed cleanup, a cancellation, or an internal error.
func autoRetryable(code FailureCode) bool {
	return code == FailureRuntime || code == FailureProbe
}

// unfixableCause reports a failure inside the cause that a retry cannot fix.
// AUTO_SELECT reports PROBE_FAILED for the whole attempt; each profile's own
// error stays in the cause. Errors without a code, such as a resolver or
// connection error from a protocol runtime, are treated as fixable.
func unfixableCause(err error) bool {
	if domain, ok := err.(*Error); ok && !autoRetryable(domain.Code) { //nolint:errorlint // every joined cause is inspected, errors.As stops at the first
		return true
	}
	switch inner := err.(type) { //nolint:errorlint // walks the joined causes one by one
	case interface{ Unwrap() []error }:
		for _, e := range inner.Unwrap() {
			if unfixableCause(e) {
				return true
			}
		}
	case interface{ Unwrap() error }:
		return unfixableCause(inner.Unwrap())
	}
	return false
}

// scheduleAutoRetryLocked arms the next attempt for a session that has just
// become FAILED with its tunnel kept, after the given cause.
func (m *Manager) scheduleAutoRetryLocked(s *session, cause error) {
	m.cancelAutoRetryLocked(s)
	if !canRetry(s) || s.stopRequested || !autoRetryable(s.lastFailure) || unfixableCause(cause) {
		return
	}
	delay := autoRetryDelay(s.autoRetryCount)
	s.autoRetryAt = m.now().Add(delay)
	token := s.autoRetryToken
	s.stopAutoRetry = m.afterFunc(delay, func() { m.runAutoRetry(s, token) })
	log.Info("SESSION", "automatic retry scheduled", map[string]any{
		"attempt": s.autoRetryCount + 1, "delay_ms": delay.Milliseconds(), "last_failure": string(s.lastFailure),
	})
}

// cancelAutoRetryLocked drops a scheduled attempt; a timer that already fired
// finds a different token and does nothing.
func (m *Manager) cancelAutoRetryLocked(s *session) {
	s.autoRetryToken++
	if s.stopAutoRetry != nil {
		s.stopAutoRetry()
		s.stopAutoRetry = nil
	}
	s.autoRetryAt = time.Time{}
}

// resetAutoRetryLocked also restarts the pause schedule and ends the retry
// chain. It runs when the user starts, retries or stops, and when the
// subscription changes.
func (m *Manager) resetAutoRetryLocked(s *session) {
	m.cancelAutoRetryLocked(s)
	s.autoRetryCount = 0
	s.autoRetryOrigin, s.autoRetryGeneration = 0, 0
}

// autoRetryStopAccepted lets a Stop for any generation of the current retry
// chain stop the session: a client may still hold the FAILED generation it
// saw before a retry started, or even connected, and the user's Stop must win
// over a retry. The chain lasts until the user acts.
func autoRetryStopAccepted(s *session, generation uint64) bool {
	return s.autoRetryOrigin != 0 && generation >= s.autoRetryOrigin && generation <= s.generation
}

// cancelAutoRetryAttemptLocked ends an automatic attempt that is still
// connecting, for example when its subscription was replaced. It finishes as
// a canceled start, which is not retried. AUTO_SELECT moves to a new
// generation for each further profile of the same attempt.
func (m *Manager) cancelAutoRetryAttemptLocked(s *session) {
	if s.autoRetryGeneration != 0 && s.generation >= s.autoRetryGeneration &&
		(s.state == StateProbing || s.state == StatePreparing) && s.cancel != nil {
		s.cancel()
	}
}

// connectedForAutoRetryLocked marks the automatic attempt finished when it
// connects and resets the pause schedule only once the connection proved
// healthy. The chain's origin stays, so a Stop sent for the FAILED generation
// still stops the connected session.
func (m *Manager) connectedForAutoRetryLocked(s *session) {
	m.cancelAutoRetryLocked(s)
	s.autoRetryGeneration = 0
	if s.autoRetryCount == 0 {
		return
	}
	token, generation := s.autoRetryToken, s.generation
	s.stopAutoRetry = m.afterFunc(autoRetryProvenHealthy, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if token == s.autoRetryToken && generation == s.generation && s.state == StateConnected {
			s.stopAutoRetry, s.autoRetryCount = nil, 0
		}
	})
}

func (m *Manager) runAutoRetry(s *session, token uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if token != s.autoRetryToken || !canRetry(s) || s.stopRequested || !autoRetryable(s.lastFailure) {
		return
	}
	s.stopAutoRetry, s.autoRetryAt = nil, time.Time{}
	s.autoRetryCount++
	log.Info("SESSION", "automatic retry started", map[string]any{"attempt": s.autoRetryCount})
	if s.autoRetryOrigin == 0 {
		s.autoRetryOrigin = s.generation
	}
	s.autoRetryGeneration = m.restartFailedLocked(s)
}
