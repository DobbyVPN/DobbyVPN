package mobilebinding

import "fmt"

// oneShotFDs separates descriptor ownership from platform mechanics so the
// no-reuse invariant is testable without a mobile/native toolchain.
type oneShotFDs struct {
	pending map[string]int32
	open    map[int32]fdOwner
}

type fdOwner struct {
	session    string
	generation uint64
}

func newOneShotFDs() oneShotFDs {
	return oneShotFDs{pending: make(map[string]int32), open: make(map[int32]fdOwner)}
}
func (o *oneShotFDs) queue(sessionID string, fd int32) error {
	if sessionID == "" || fd < 0 {
		return fmt.Errorf("invalid one-shot tunnel")
	}
	if _, exists := o.pending[sessionID]; exists {
		return fmt.Errorf("one-shot tunnel already queued")
	}
	o.pending[sessionID] = fd
	return nil
}
func (o *oneShotFDs) take(sessionID string) (int32, bool) {
	fd, ok := o.pending[sessionID]
	if ok {
		delete(o.pending, sessionID)
	}
	return fd, ok
}
func (o *oneShotFDs) reserve(fd int32, owner fdOwner) bool {
	if _, exists := o.open[fd]; exists {
		return false
	}
	o.open[fd] = owner
	return true
}
func (o *oneShotFDs) release(fd int32, owner fdOwner) {
	if current, ok := o.open[fd]; ok && current == owner {
		delete(o.open, fd)
	}
}
