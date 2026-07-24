package routing

import (
	"errors"
	"fmt"
	"sync"

	"go_module/log"
)

// Plan owns the routing resources acquired for one VPN session.  Resources are
// released in reverse acquisition order, and each Lease is idempotent.  A Plan
// deliberately has no process-global cleanup: it can only release resources it
// successfully acquired itself.
type Plan struct {
	sessionID string

	mu     sync.Mutex
	closed bool
	leases []*Lease
}

// Lease represents one exact route, rule, or firewall resource installed by a
// Plan. Close may safely be called repeatedly and concurrently.
type Lease struct {
	name string

	once    sync.Once
	release func() error
	err     error
}

func NewPlan(sessionID string) *Plan {
	return &Plan{sessionID: sessionID}
}

func (p *Plan) SessionID() string { return p.sessionID }

// Acquire runs apply while the plan is serialized, then immediately records
// release. This closes the gap where a successfully-created route could be
// lost before cleanup knows it exists.
func (p *Plan) Acquire(name string, apply, release func() error) (*Lease, error) {
	if apply == nil || release == nil {
		return nil, fmt.Errorf("routing plan %q: %s requires apply and release", p.sessionID, name)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, fmt.Errorf("routing plan %q is already closed", p.sessionID)
	}
	if err := apply(); err != nil {
		return nil, fmt.Errorf("routing plan %q acquire %s: %w", p.sessionID, name, err)
	}

	lease := &Lease{name: name, release: release}
	p.leases = append(p.leases, lease)
	log.Debugf(Category, "[Plan] session=%s acquired=%s", p.sessionID, name)
	return lease, nil
}

func (l *Lease) Name() string { return l.name }

func (l *Lease) Close() error {
	l.once.Do(func() {
		l.err = l.release()
	})
	return l.err
}

// Close releases only this Plan's leases in LIFO order. It is safe to invoke
// from both startup rollback and normal shutdown; the underlying releases run
// once even if those paths race.
func (p *Plan) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	leases := append([]*Lease(nil), p.leases...)
	p.mu.Unlock()

	var errs []error
	for index := len(leases) - 1; index >= 0; index-- {
		lease := leases[index]
		if err := lease.Close(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", lease.name, err))
			continue
		}
		log.Debugf(Category, "[Plan] session=%s released=%s", p.sessionID, lease.name)
	}
	return errors.Join(errs...)
}
