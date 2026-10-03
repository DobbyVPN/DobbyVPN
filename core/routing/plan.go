package routing

import (
	"errors"
	"fmt"
	"sync"

	"core/log"
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

	mu      sync.Mutex
	closed  bool
	release func() error
	repair  func() (bool, error)
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
	log.Debugf(Category, "[Plan] session_owned=true acquired=%s", name)
	return lease, nil
}

func (l *Lease) Name() string { return l.name }

func (l *Lease) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	if err := l.release(); err != nil {
		return err
	}
	l.closed = true
	return nil
}

// Close serializes LIFO release. Failed leases stay owned and can be retried;
// successful releases are never repeated.
func (p *Plan) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	var errs []error
	for index := len(p.leases) - 1; index >= 0; index-- {
		lease := p.leases[index]
		if err := lease.Close(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", lease.name, err))
			continue
		}
		log.Debugf(Category, "[Plan] session_owned=true released=%s", lease.name)
		p.leases = append(p.leases[:index], p.leases[index+1:]...)
	}
	return errors.Join(errs...)
}

// Repair verifies retained route identities before restoring only absent routes.
func (p *Plan) Repair() (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false, nil
	}
	repaired := false
	for _, lease := range p.leases {
		lease.mu.Lock()
		changed, err := false, error(nil)
		if lease.repair != nil {
			changed, err = lease.repair()
		}
		lease.mu.Unlock()
		if err != nil {
			return repaired, err
		}
		repaired = repaired || changed
	}
	return repaired, nil
}
