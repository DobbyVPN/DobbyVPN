package platform_engine

import (
	"sync"
	"time"
)

// tcpAdmission belongs to a single stack. A connection is admitted when its
// slot is reserved under mu. Sealing the stack prevents any further admission.
type tcpAdmission struct {
	mu              sync.Mutex
	active, pending int
	waited, refused uint64
	sealed          bool
	changed         chan struct{}
}

func (a *tcpAdmission) signal() {
	close(a.changed)
	a.changed = make(chan struct{})
}

func (a *tcpAdmission) seal() (waited, refused uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sealed = true
	a.signal()
	return a.waited, a.refused
}

func (a *tcpAdmission) acquire(activeLimit, pendingLimit int, wait time.Duration) func() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sealed {
		return nil
	}
	if a.active >= activeLimit {
		if a.pending >= pendingLimit {
			a.refused++
			return nil
		}
		a.pending++
		a.waited++
		defer func() { a.pending-- }()
		deadline := time.Now().Add(wait)
		timer := time.NewTimer(wait)
		defer timer.Stop()
		for a.active >= activeLimit && !a.sealed {
			changed := a.changed
			a.mu.Unlock()
			select {
			case <-changed:
			case <-timer.C:
			}
			a.mu.Lock()
			if !time.Now().Before(deadline) {
				a.refused++
				return nil
			}
		}
		if a.sealed {
			return nil
		}
	}

	a.active++
	return sync.OnceFunc(func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.active--
		a.signal()
	})
}
