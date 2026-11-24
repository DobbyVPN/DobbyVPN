//go:build !(android || ios)

package executor

import (
	"context"
	"sync"
	"time"

	"core/log"
	"core/sessionapi/mobilebinding"
)

const (
	// One network change produces a burst of OS notifications that should reach
	// the core as a single event.
	networkEventDebounce = time.Second
	eventWake            = "wake"
	eventNetworkChanged  = "network_path_changed"
)

// networkEvents holds one pending reason. The first event arms a timer; events
// that arrive before it fires only upgrade the reason, and "wake" wins over a
// network change.
type networkEvents struct {
	mu       sync.Mutex
	pending  string
	debounce time.Duration
	send     func(reason string)
}

// newNetworkEvents returns the emit function for OS watchers; it never blocks.
func newNetworkEvents() func(reason string) {
	return (&networkEvents{debounce: networkEventDebounce, send: func(reason string) {
		sendNetworkEvent(desktopProcessBinding(), reason)
	}}).emit
}

func (e *networkEvents) emit(reason string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pending == "" {
		e.pending = reason
		time.AfterFunc(e.debounce, e.flush)
	} else if reason == eventWake {
		e.pending = eventWake
	}
}

func (e *networkEvents) flush() {
	e.mu.Lock()
	reason := e.pending
	e.pending = ""
	e.mu.Unlock()
	e.send(reason)
}

func sendNetworkEvent(binding *mobilebinding.Binding, reason string) {
	log.Infof(desktopLogCategory, "system event: %s", reason)
	// An empty session ID selects the process-owned session.
	if err := binding.NetworkEvent(context.Background(), "", reason); err != nil {
		log.Errorf(desktopLogCategory, "system event %s was not accepted: %v", reason, err)
	}
}
