package routing

import (
	"context"
	"fmt"
	"sync"
	"time"

	"core/log"
)

const (
	durationKey = "duration_ms"
	resourceKey = "resource"
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
	release func(context.Context) error
	repair  func() (bool, error)
}

func NewPlan(sessionID string) *Plan {
	return &Plan{sessionID: sessionID}
}

func (p *Plan) SessionID() string { return p.sessionID }

// Acquire runs apply while the plan is serialized, then immediately records
// release. This closes the gap where a successfully-created route could be
// lost before cleanup knows it exists.
func (p *Plan) Acquire(name string, apply func() error, release func(context.Context) error) (*Lease, error) {
	if apply == nil || release == nil {
		return nil, fmt.Errorf("routing plan %q: %s requires apply and release", p.sessionID, name)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, fmt.Errorf("routing plan %q is already closed", p.sessionID)
	}
	started := time.Now()
	if err := apply(); err != nil {
		log.Error(Category, "routing resource acquisition failed", map[string]any{resourceKey: name, durationKey: time.Since(started).Milliseconds(), "cause": err.Error()})
		return nil, fmt.Errorf("routing plan %q acquire %s: %w", p.sessionID, name, err)
	}

	lease := &Lease{name: name, release: release}
	p.leases = append(p.leases, lease)
	log.Info(Category, "routing resource acquired", map[string]any{resourceKey: name, durationKey: time.Since(started).Milliseconds()})
	return lease, nil
}

func (l *Lease) Name() string { return l.name }

func (l *Lease) Close(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	started := time.Now()
	if err := l.release(ctx); err != nil {
		log.Error(Category, "routing resource release pending", map[string]any{resourceKey: l.name, durationKey: time.Since(started).Milliseconds(), "cause": err.Error()})
		return err
	}
	log.Info(Category, "routing resource released", map[string]any{resourceKey: l.name, durationKey: time.Since(started).Milliseconds()})
	l.closed = true
	return nil
}

// Close serializes LIFO release. Failed leases stay owned and can be retried;
// successful releases are never repeated.
func (p *Plan) Close(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	for len(p.leases) > 0 {
		index := len(p.leases) - 1
		lease := p.leases[index]
		if err := lease.Close(ctx); err != nil {
			return fmt.Errorf("%s: %w", lease.name, err)
		}
		p.leases = p.leases[:index]
	}
	return nil
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
