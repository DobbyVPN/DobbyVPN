package platform_engine

import (
	"sync"
	"time"
)

// tcpAdmission belongs to a single stack. A connection is admitted when its
// slot is reserved under mu. Waiting connections are admitted in the order
// they entered acquire: a freed slot is handed to the oldest waiter that has
// not timed out, and a new connection queues behind anyone already waiting.
// gVisor runs each SYN's callback on its own goroutine, so this is the order
// in which callbacks reach acquire, not strictly the order SYNs arrived.
// Sealing the stack prevents any further admission.
type tcpAdmission struct {
	mu                        sync.Mutex
	activeLimit, pendingLimit int
	wait                      time.Duration
	active                    int
	queue                     []*tcpWaiter
	waited, refused           uint64
	epoch                     uint64 // advanced when pending admissions are canceled
	sealed                    bool
	paused                    bool
}

type tcpWaiter struct {
	ready    chan struct{}
	deadline time.Time
	granted  bool
}

func newTCPAdmission(activeLimit, pendingLimit int, wait time.Duration) *tcpAdmission {
	return &tcpAdmission{activeLimit: activeLimit, pendingLimit: pendingLimit, wait: wait}
}

// acquire returns the release function of an admitted connection, or nil when
// the connection is refused: pending bound reached, wait expired, or the
// pending admissions were canceled by seal or cancelPending. A connection
// that had to wait also gets the epoch it waited in; canceledSince reports
// whether pending admissions were canceled after that, so the caller can still
// refuse it before it is dispatched.
func (a *tcpAdmission) acquire() (release func(), epoch uint64, waited bool) {
	a.mu.Lock()
	if a.sealed {
		a.mu.Unlock()
		return nil, 0, false
	}
	if !a.paused && a.active < a.activeLimit && len(a.queue) == 0 {
		a.active++
		a.mu.Unlock()
		return a.releaser(), 0, false
	}
	if len(a.queue) >= a.pendingLimit {
		a.refused++
		a.mu.Unlock()
		return nil, 0, false
	}
	w := &tcpWaiter{ready: make(chan struct{}), deadline: time.Now().Add(a.wait)}
	a.queue = append(a.queue, w)
	a.waited++
	epoch = a.epoch
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
		return a.releaser(), epoch, true
	}
	// Still queued means the wait expired; otherwise it was already refused.
	for i, queued := range a.queue {
		if queued == w {
			a.queue = append(a.queue[:i], a.queue[i+1:]...)
			a.refused++
			break
		}
	}
	return nil, 0, false
}

// canceledSince reports whether pending admissions were canceled after epoch.
func (a *tcpAdmission) canceledSince(epoch uint64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sealed || a.epoch != epoch
}

// refuse releases the slot of an admitted connection that is answered with
// RST instead of being dispatched, and counts it as refused. release is a
// once-function, so the slot cannot be returned twice.
func (a *tcpAdmission) refuse(release func()) {
	a.mu.Lock()
	a.refused++
	a.mu.Unlock()
	release()
}

func (a *tcpAdmission) releaser() func() {
	return sync.OnceFunc(func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.active--
		a.handOff()
	})
}

// handOff gives free slots to the oldest waiters. A waiter whose wait has
// expired is refused even if its timer has not taken the lock yet. Caller
// holds mu.
func (a *tcpAdmission) handOff() {
	if a.paused || a.sealed {
		return
	}
	now := time.Now()
	for a.active < a.activeLimit && len(a.queue) > 0 {
		w := a.queue[0]
		a.queue = a.queue[1:]
		if !now.Before(w.deadline) {
			a.refused++
		} else {
			w.granted = true
			a.active++
		}
		close(w.ready)
	}
}

func (a *tcpAdmission) setPaused(paused bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.paused = paused
	a.handOff()
}

// dropPending refuses every waiting connection; each one is answered with RST
// and counted as refused. Caller holds mu.
func (a *tcpAdmission) dropPending() {
	for _, w := range a.queue {
		close(w.ready)
	}
	a.refused += uint64(len(a.queue))
	a.queue = nil
}

// cancelPending refuses the connections waiting now, as seal does, but keeps
// admitting new ones. Active connections are left to their owner.
func (a *tcpAdmission) cancelPending() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.epoch++
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
