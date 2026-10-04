package sessionapi

import (
	"context"
	"sync"
	"time"
)

// CleanupTimeout is the total budget from the first stop/rollback request,
// including any acquisition that still has to finish. Nested owners inherit it.
const CleanupTimeout = 30 * time.Second

type cleanupBudgetKey struct{}
type cleanupBudget struct {
	once   sync.Once
	ctx    context.Context
	cancel context.CancelFunc
}

func (b *cleanupBudget) begin() context.Context {
	b.once.Do(func() {
		// The timer expires by itself; all holders share this one deadline.
		b.ctx, b.cancel = context.WithTimeout(context.Background(), CleanupTimeout)
	})
	return b.ctx
}

func (b *cleanupBudget) finish() {
	if b != nil && b.cancel != nil {
		b.cancel()
	}
}

// CleanupContext ignores acquisition cancellation but preserves its owner's
// cleanup deadline. A direct runtime caller also gets a bounded rollback.
func CleanupContext(ctx context.Context) context.Context {
	if budget, ok := ctx.Value(cleanupBudgetKey{}).(*cleanupBudget); ok {
		return budget.begin()
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx
	}
	return (&cleanupBudget{}).begin()
}

func (m *Manager) cleanupContext(s *session) context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cleanupBudget == nil {
		s.cleanupBudget = &cleanupBudget{}
	}
	return s.cleanupBudget.begin()
}

// StopAndWait is an internal process/service lifecycle operation, never a
// desktop wire method. It fences new work before asking the existing attempt
// worker to release its resources, and waits for confirmed completion.
func (m *Manager) StopAndWait(ctx context.Context) error {
	s := m.session
	s.mu.Lock()
	s.closing = true
	s.pending = nil
	s.recovering, s.restartAfterCleanup, s.recoveryOriginGeneration = false, false, 0
	if !s.cleanupDone && s.state != StateFailed {
		s.state = StateStopping
		if s.cleanupBudget == nil {
			s.cleanupBudget = &cleanupBudget{}
		}
		s.cleanupBudget.begin()
		if s.cancel != nil {
			s.cancel()
		}
	}
	m.appendLocked(s)
	s.mu.Unlock()
	budget := m.cleanupContext(s)
	for {
		s.mu.Lock()
		complete, failed, message, changed := s.cleanupDone, s.cleanupFailed, s.lastFailureMessage, s.changed
		s.mu.Unlock()
		if failed {
			return failure(FailureCleanup, message)
		}
		if complete {
			return nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return failureWithCause(FailureCleanup, "shutdown still owns resources", ctx.Err())
		case <-budget.Done():
			s.mu.Lock()
			complete = s.cleanupDone && !s.cleanupFailed
			s.mu.Unlock()
			if complete {
				return nil
			}
			return failureWithCause(FailureCleanup, "shutdown still owns resources", budget.Err())
		}
	}
}

// Resume permits a new mobile service attachment only after the previous
// service's resources were confirmed released. Desktop shutdown never resumes.
func (m *Manager) Resume() error {
	s := m.session
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closing {
		return nil
	}
	if !s.cleanupDone || s.cleanupFailed {
		return failure(FailureCleanup, "previous service still owns resources")
	}
	s.closing = false
	m.appendLocked(s)
	return nil
}
