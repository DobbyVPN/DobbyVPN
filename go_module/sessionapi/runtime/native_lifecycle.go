package runtime

import (
	"errors"
	"fmt"
	"go_module/log"
	"sync"
)

// lifecycleState is the internal phase of one native resource adapter. It is
// not a public session state; the session manager owns the externally meaningful state
// and generation contract.
type lifecycleState string

const (
	stateIdle      lifecycleState = "IDLE"
	statePreparing lifecycleState = "PREPARING"
	stateConnected lifecycleState = "CONNECTED"
	stateStopping  lifecycleState = "STOPPING"
	stateFailed    lifecycleState = "FAILED"
)

func (s lifecycleState) String() string { return string(s) }

func lifecycleBusyError(state lifecycleState) error {
	return fmt.Errorf("native session runtime lifecycle is %s", state)
}

// runLockedWithPanicRecovery owns one mutex-protected operation and invokes
// cleanup while that mutex is still held. Cleanup must therefore be the
// lock-held form of the operation's rollback; calling the public method that
// acquires mu again would deadlock during panic recovery.
func runLockedWithPanicRecovery(
	label string,
	mu *sync.Mutex,
	operation func() error,
	cleanup func() error,
) (err error) {
	mu.Lock()
	defer mu.Unlock()
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Debugf(nativeLogCategory, "recovered from %s panic: %v", label, recovered)
			var cleanupErr error
			if cleanup != nil {
				cleanupErr = cleanup()
			}
			err = errors.Join(fmt.Errorf("%s panic: %v", label, recovered), cleanupErr)
		}
	}()
	return operation()
}

const nativeLogCategory = "RUNTIME"
