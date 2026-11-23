package platform_engine

import (
	"sync"
	"time"
)

// tcpAdmission belongs to a single stack. A connection is admitted when its
// slot is reserved under mu. Waiting connections are admitted in arrival
// order: a freed slot is handed to the oldest waiter, and a new connection
// queues behind anyone already waiting. Sealing the stack prevents any
// further admission.
type tcpAdmission struct {
	mu                        sync.Mutex
	activeLimit, pendingLimit int
	wait                      time.Duration
	active                    int
	queue                     []*tcpWaiter
	waited, refused           uint64
	sealed                    bool
}

type tcpWaiter struct {
	ready   chan struct{}
	granted bool
}

func newTCPAdmission(activeLimit, pendingLimit int, wait time.Duration) *tcpAdmission {
	return &tcpAdmission{activeLimit: activeLimit, pendingLimit: pendingLimit, wait: wait}
}

// acquire returns the release function of an admitted connection, or nil when
// the connection is refused: pending bound reached, wait expired, or the
// pending admissions were canceled by seal or cancelPending.
func (a *tcpAdmission) acquire() func() {
	a.mu.Lock()
	if a.sealed {
		a.mu.Unlock()
		return nil
	}
	if a.active < a.activeLimit && len(a.queue) == 0 {
		a.active++
		a.mu.Unlock()
		return a.releaser()
	}
	if len(a.queue) >= a.pendingLimit {
		a.refused++
		a.mu.Unlock()
		return nil
	}
	w := &tcpWaiter{ready: make(chan struct{})}
	a.queue = append(a.queue, w)
	a.waited++
	a.mu.Unlock()

	timer := time.NewTimer(a.wait)
	defer timer.Stop()
	select {
	case <-w.ready:
	case <-timer.C:
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if w.granted {
		return a.releaser()
	}
	// Still queued means the wait expired; otherwise it was canceled.
	for i, queued := range a.queue {
		if queued == w {
			a.queue = append(a.queue[:i], a.queue[i+1:]...)
			a.refused++
			break
		}
	}
	return nil
}

func (a *tcpAdmission) releaser() func() {
	return sync.OnceFunc(func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.active--
		for a.active < a.activeLimit && len(a.queue) > 0 {
			w := a.queue[0]
			a.queue = a.queue[1:]
			w.granted = true
			a.active++
			close(w.ready)
		}
	})
}

// dropPending refuses every waiting connection. Caller holds mu.
func (a *tcpAdmission) dropPending() {
	for _, w := range a.queue {
		close(w.ready)
	}
	a.queue = nil
}

// cancelPending refuses the connections waiting now, as seal does, but keeps
// admitting new ones. Active connections are left to their owner.
func (a *tcpAdmission) cancelPending() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.dropPending()
}

func (a *tcpAdmission) seal() (waited, refused uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sealed = true
	a.dropPending()
	return a.waited, a.refused
}

func (a *tcpAdmission) stats() (active, pending int, waited, refused uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.active, len(a.queue), a.waited, a.refused
}
