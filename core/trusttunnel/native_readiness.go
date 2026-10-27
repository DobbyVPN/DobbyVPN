package trusttunnel

import (
	"context"
	"errors"
	"fmt"
	"sync"

	tt "trusttunnel-go/manager"
)

// The native bridge and callback trampoline are process-global. Retirement
// must finish before another device rebinds them.
var nativeOwner = make(chan struct{}, 1)

var errNativeStopped = errors.New("TrustTunnel native transport stopped")

type nativeReadiness struct {
	mu       sync.Mutex
	changed  chan struct{}
	state    tt.VpnState
	observed bool
	closed   bool
}

func newNativeReadiness() *nativeReadiness {
	return &nativeReadiness{changed: make(chan struct{})}
}

func (r *nativeReadiness) update(state tt.VpnState) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.observed && r.state == state {
		return false
	}
	r.state, r.observed = state, true
	close(r.changed)
	r.changed = make(chan struct{})
	return true
}

func (r *nativeReadiness) wait(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			return errNativeStopped
		}
		if r.observed && r.state == tt.StateConnected {
			r.mu.Unlock()
			return nil
		}
		if r.observed && r.state == tt.StateError {
			r.mu.Unlock()
			return errors.New("TrustTunnel native transport reported an error")
		}
		changed := r.changed
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (r *nativeReadiness) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.closed = true
		close(r.changed)
	}
}

func nativeStateName(state tt.VpnState) string {
	switch state {
	case tt.StateDisconnected:
		return "disconnected"
	case tt.StateConnecting:
		return "connecting"
	case tt.StateConnected:
		return "connected"
	case tt.StateReconnecting:
		return "reconnecting"
	case tt.StateError:
		return "error"
	default:
		return fmt.Sprintf("unknown(%d)", state)
	}
}
