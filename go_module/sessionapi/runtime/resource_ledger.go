package runtime

import (
	"errors"
	"sync"
)

// resourceLedger records cleanup immediately after an acquisition succeeds.
// Rollback is idempotent and executes active cleanup functions in LIFO order.
// A release function disarms an entry after ownership is transferred elsewhere.
type resourceLedger struct {
	mu      sync.Mutex
	entries []*ledgerEntry
	closed  bool
}

type ledgerEntry struct {
	rollback func() error
	active   bool
}

func (l *resourceLedger) Add(rollback func() error) (release func()) {
	if rollback == nil {
		return func() {}
	}
	l.mu.Lock()
	entry := &ledgerEntry{rollback: rollback, active: !l.closed}
	l.entries = append(l.entries, entry)
	l.mu.Unlock()
	return func() {
		l.mu.Lock()
		entry.active = false
		l.mu.Unlock()
	}
}

func (l *resourceLedger) Rollback() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	entries := append([]*ledgerEntry(nil), l.entries...)
	l.mu.Unlock()

	var errs []error
	for i := len(entries) - 1; i >= 0; i-- {
		l.mu.Lock()
		active := entries[i].active
		entries[i].active = false
		l.mu.Unlock()
		if active {
			errs = append(errs, entries[i].rollback())
		}
	}
	return errors.Join(errs...)
}
